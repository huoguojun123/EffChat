package handler

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/huoguojun123/EffChat/internal/extractor"
	"github.com/huoguojun123/EffChat/internal/filepolicy"
	"github.com/huoguojun123/EffChat/internal/model"
	"github.com/huoguojun123/EffChat/internal/repository"
	"github.com/huoguojun123/EffChat/internal/service"
)

const (
	ocrRecoveryInterval = 5 * time.Second
	ocrRetryDelay       = 15 * time.Second
	ocrSourceRetention  = 24 * time.Hour
	ocrMaxAttempts      = 5
)

type OCRRecoveryRunner struct {
	fileRepo        *repository.FileRepository
	channelService  *service.ChannelService
	extractorClient *extractor.SidecarClient
	quotaService    *service.QuotaService
	configRepo      *repository.ConfigRepository
	wake            chan struct{}
	cancel          context.CancelFunc
	startOnce       sync.Once
	stopOnce        sync.Once
	loopWG          sync.WaitGroup
	workerWG        sync.WaitGroup
	stopped         chan struct{}
}

func NewOCRRecoveryRunner(fileRepo *repository.FileRepository, channelService *service.ChannelService, extractorClient *extractor.SidecarClient, quotaService *service.QuotaService, configRepo *repository.ConfigRepository) *OCRRecoveryRunner {
	return &OCRRecoveryRunner{
		fileRepo:        fileRepo,
		channelService:  channelService,
		extractorClient: extractorClient,
		quotaService:    quotaService,
		configRepo:      configRepo,
		wake:            make(chan struct{}, 1),
		stopped:         make(chan struct{}),
	}
}

func (r *OCRRecoveryRunner) Start() {
	if r == nil || r.fileRepo == nil {
		return
	}
	r.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		r.cancel = cancel
		r.loopWG.Add(1)
		go func() {
			defer r.loopWG.Done()
			r.loop(ctx)
		}()
		r.Wake()
	})
}

func (r *OCRRecoveryRunner) Drain(ctx context.Context) bool {
	if r == nil {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.stopOnce.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
		go func() {
			r.loopWG.Wait()
			r.workerWG.Wait()
			close(r.stopped)
		}()
	})
	select {
	case <-r.stopped:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *OCRRecoveryRunner) Wake() {
	if r == nil {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *OCRRecoveryRunner) loop(ctx context.Context) {
	ticker := time.NewTicker(ocrRecoveryInterval)
	defer ticker.Stop()
	for {
		r.recoverOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.wake:
		}
	}
}

func (r *OCRRecoveryRunner) recoverOnce(ctx context.Context) {
	now := time.Now()
	if _, err := r.fileRepo.FailStaleOCRSubmissions(now); err != nil {
		log.Printf("[file_ocr] stale_submission_reconcile_failed err=%v", err)
	}
	if r.channelService == nil || r.extractorClient == nil || !r.extractorClient.Enabled() {
		return
	}
	cfg := r.channelService.ResolveMinerUOCRConfig()
	if !cfg.Enabled {
		return
	}
	files, err := r.fileRepo.ClaimRecoverableOCRTasks("mineru", now, r.taskLease(), cfg.MaxConcurrency)
	if err != nil {
		log.Printf("[file_ocr] recovery_claim_failed err=%v", err)
		return
	}
	for _, file := range files {
		file := file
		if ctx.Err() != nil {
			return
		}
		r.startWorker(func() {
			r.process(ctx, cfg, file)
		})
	}
}

func (r *OCRRecoveryRunner) startWorker(run func()) {
	r.workerWG.Add(1)
	go func() {
		defer r.workerWG.Done()
		run()
	}()
}

func (r *OCRRecoveryRunner) taskLease() time.Duration {
	return 2*minerUStartTimeout(r.extractTimeoutSeconds()) + time.Minute
}

func (r *OCRRecoveryRunner) process(ctx context.Context, cfg service.MinerUOCRConfig, file *model.File) {
	if file == nil {
		return
	}
	if file.OCRTaskID == nil || strings.TrimSpace(*file.OCRTaskID) == "" {
		r.submit(ctx, cfg, file)
		return
	}
	r.poll(ctx, cfg, file)
}

func (r *OCRRecoveryRunner) submit(ctx context.Context, cfg service.MinerUOCRConfig, file *model.File) {
	attempts, err := r.fileRepo.RecordOCRAttempt(file.ID, file.UserID)
	if err != nil {
		log.Printf("[file_ocr] attempt_record_failed file_id=%d err=%v", file.ID, err)
		return
	}
	sourcePath, err := filepolicy.ExistingPath(valueOrEmpty(file.OCRSourcePath))
	if err != nil {
		_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_source_missing", "OCR 原文件不存在，无法继续解析")
		return
	}
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_source_missing", "OCR 原文件不存在，无法继续解析")
			return
		}
		r.scheduleOCRRetry(file, attempts, "read_source_failed", err)
		return
	}
	inspectCtx, cancel := context.WithTimeout(ctx, minerUStartTimeout(r.extractTimeoutSeconds()))
	info, err := r.extractorClient.InspectOCRPDF(inspectCtx, content, file.FileName)
	cancel()
	if err != nil {
		if isPermanentOCRSubmitError(err) {
			_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_submit_rejected", humanizeOCRError(err))
			return
		}
		r.scheduleOCRRetry(file, attempts, "inspect_failed", err)
		return
	}
	if info.PageCount > 200 {
		_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_page_limit_exceeded", "PDF 超过 MinerU 精准解析 200 页限制")
		return
	}
	reserved := true
	if r.quotaService != nil {
		reserved, err = r.quotaService.ReserveOCRSubmission(ctx, file.ID, file.UserID, info.PageCount)
	} else {
		reserved, err = r.fileRepo.MarkOCRSubmissionStarted(file.ID, file.UserID)
	}
	if err != nil {
		if _, ok := err.(*service.QuotaError); ok {
			_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_quota_exceeded", "OCR 配额已用完，请稍后再试")
			return
		}
		log.Printf("[file_ocr] submission_reservation_failed file_id=%d err=%v", file.ID, err)
		return
	}
	if !reserved {
		return
	}
	startCtx, cancel := context.WithTimeout(ctx, minerUStartTimeout(r.extractTimeoutSeconds()))
	start, err := r.extractorClient.StartMinerUOCR(startCtx, content, file.FileName, cfg.BaseURL, cfg.APIKey)
	cancel()
	if err != nil {
		if isPermanentOCRSubmitError(err) {
			_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_submit_rejected", humanizeOCRError(err))
			return
		}
		if isUncertainOCRSubmissionError(err) {
			_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_submission_unknown", "OCR 任务提交结果未确认，请在服务恢复后手动重试")
			return
		}
		r.scheduleOCRRetry(file, attempts, "submit_failed", err)
		return
	}
	pages := maxInt(info.PageCount, start.PageCount)
	ok, err := r.fileRepo.StartOCRTask(file.ID, file.UserID, start.TaskID, pages)
	if err != nil {
		log.Printf("[file_ocr] submit_state_failed file_id=%d err=%v", file.ID, err)
		_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_submission_state_unknown", "OCR 任务已提交但本地确认失败，请确认服务恢复后手动重试")
		return
	}
	if !ok {
		return
	}
	_ = r.fileRepo.ReleaseOCRLease(file.ID, file.UserID, time.Now())
	r.Wake()
}

func (r *OCRRecoveryRunner) poll(ctx context.Context, cfg service.MinerUOCRConfig, file *model.File) {
	taskID := strings.TrimSpace(valueOrEmpty(file.OCRTaskID))
	pollCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	result, err := r.extractorClient.PollMinerUOCR(pollCtx, taskID, cfg.BaseURL, cfg.APIKey)
	cancel()
	if err != nil {
		if isTransientOCRPollError(err) {
			attempts, attemptErr := r.fileRepo.RecordOCRAttempt(file.ID, file.UserID)
			if attemptErr != nil {
				log.Printf("[file_ocr] poll_attempt_record_failed file_id=%d err=%v", file.ID, attemptErr)
				return
			}
			if attempts >= ocrMaxAttempts {
				_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_retry_exhausted", "OCR 服务多次重试后仍未成功，请联系管理员或删除文件后重新上传")
				log.Printf("[file_ocr] poll_retry_exhausted file_id=%d attempts=%d cause=%v", file.ID, attempts, err)
				return
			}
			_ = r.fileRepo.UpdateOCRRunning(file.ID, file.UserID, file.OCRProgressPages)
			_ = r.fileRepo.ReleaseOCRLease(file.ID, file.UserID, time.Now().Add(ocrRetryDelayFor(attempts)))
			return
		}
		_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_upstream_failed", humanizeOCRError(err))
		return
	}
	switch result.State {
	case "ready":
		r.complete(file, result)
	case "failed":
		_ = r.fileRepo.FailOCR(file.ID, file.UserID, result.ErrorType, minerUFailedTaskMessage(result.ErrorType))
	default:
		_ = r.fileRepo.UpdateOCRRunning(file.ID, file.UserID, file.OCRProgressPages)
		_ = r.fileRepo.ReleaseOCRLease(file.ID, file.UserID, time.Now().Add(ocrRecoveryInterval))
	}
}

func (r *OCRRecoveryRunner) complete(file *model.File, result *extractor.MinerUPollResult) {
	if result == nil {
		_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_empty_result", "OCR 未返回可读文本，请重试或删除文件")
		return
	}
	if int64(len([]byte(result.Markdown))) > r.maxOutputBytes() {
		_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_output_too_large", "OCR 解析结果过大，请上传更小的文件")
		return
	}
	if strings.TrimSpace(result.Markdown) == "" {
		_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_empty_result", "OCR 未返回可读文本，请重试或删除文件")
		return
	}
	if err := writeTextFile(file.FilePath, result.Markdown); err != nil {
		_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_write_failed", "OCR 结果保存失败，请重试")
		return
	}
	ok, err := r.fileRepo.CompleteOCR(file.ID, file.UserID, file.FilePath, result.TokenEstimate)
	if err != nil || !ok {
		_ = filepolicy.Remove(file.FilePath)
		if err != nil {
			log.Printf("[file_ocr] complete_state_failed file_id=%d err=%v", file.ID, err)
		}
		return
	}
	if sourcePath := valueOrEmpty(file.OCRSourcePath); sourcePath != "" {
		if err := removeManagedFilePaths("", nil, file.OCRSourcePath); err != nil {
			log.Printf("[file_ocr] source_cleanup_failed file_id=%d err=%v", file.ID, err)
			return
		}
		if err := r.fileRepo.ClearOCRSourcePath(file.ID, file.UserID, sourcePath); err != nil {
			log.Printf("[file_ocr] source_state_cleanup_failed file_id=%d err=%v", file.ID, err)
		}
	}
}

func (r *OCRRecoveryRunner) scheduleOCRRetry(file *model.File, attempts int, stage string, err error) {
	if attempts >= ocrMaxAttempts {
		_ = r.fileRepo.FailOCR(file.ID, file.UserID, "ocr_retry_exhausted", "OCR 服务多次重试后仍未成功，请联系管理员或删除文件后重新上传")
		log.Printf("[file_ocr] retry_exhausted file_id=%d stage=%s attempts=%d cause=%v", file.ID, stage, attempts, err)
		return
	}
	if releaseErr := r.fileRepo.ReleaseOCRLease(file.ID, file.UserID, time.Now().Add(ocrRetryDelayFor(attempts))); releaseErr != nil {
		log.Printf("[file_ocr] retry_schedule_failed file_id=%d stage=%s err=%v", file.ID, stage, releaseErr)
		return
	}
	log.Printf("[file_ocr] retry_scheduled file_id=%d stage=%s attempts=%d cause=%v", file.ID, stage, attempts, err)
}

func ocrRetryDelayFor(attempts int) time.Duration {
	if attempts <= 1 {
		return ocrRetryDelay
	}
	shift := min(attempts-1, 4)
	return ocrRetryDelay * time.Duration(1<<shift)
}

func (r *OCRRecoveryRunner) extractTimeoutSeconds() int {
	if r.configRepo == nil {
		return 60
	}
	seconds := r.configRepo.GetInt("attachment_extract_timeout_seconds", 60)
	if seconds <= 0 {
		return 60
	}
	return seconds
}

func (r *OCRRecoveryRunner) maxOutputBytes() int64 {
	maxOutputMB := 5
	if r.configRepo != nil {
		maxOutputMB = r.configRepo.GetInt("attachment_max_output_mb", maxOutputMB)
	}
	if maxOutputMB <= 0 {
		maxOutputMB = 5
	}
	return int64(maxOutputMB) * 1024 * 1024
}
