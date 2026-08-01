package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	ocrPolicyRetryDelay = time.Minute
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
	policy, policyErr := resolveAttachmentProcessingPolicy(ctx, r.configRepo)
	if policyErr != nil {
		log.Printf("[file_ocr] attachment_policy_unavailable err=%v", policyErr)
	} else if policy.Degraded {
		log.Printf("[file_ocr] attachment policy degraded; using last-known-good controls")
	}
	files, err := r.fileRepo.ClaimRecoverableOCRTasks("mineru", now, r.taskLease(policy), cfg.MaxConcurrency)
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
			r.process(ctx, cfg, policy, policyErr, file)
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

func (r *OCRRecoveryRunner) taskLease(policy attachmentProcessingPolicy) time.Duration {
	timeoutSeconds := policy.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 60
	}
	return 2*minerUStartTimeout(timeoutSeconds) + time.Minute
}

func (r *OCRRecoveryRunner) process(ctx context.Context, cfg service.MinerUOCRConfig, policy attachmentProcessingPolicy, policyErr error, file *model.File) {
	if file == nil {
		return
	}
	// A new generation owns the work item. Its predecessor can no longer commit,
	// so a deterministic previous-generation temp is safe to reclaim without
	// ever touching the shared final sidecar.
	if file.OCRLeaseGeneration > 1 {
		_ = filepolicy.Remove(ocrSidecarTempPath(file.FilePath, file.OCRLeaseGeneration-1))
	}
	if file.OCRTaskID == nil || strings.TrimSpace(*file.OCRTaskID) == "" {
		if policyErr != nil || !policy.Enabled {
			// Pending work has not crossed the external boundary yet. Keep it
			// pending and back off instead of inspecting or submitting content;
			// a later loop automatically resumes after policy recovery/re-enable.
			if err := r.fileRepo.ReleaseOCRLease(file.ID, file.UserID, file.OCRLeaseGeneration, time.Now().Add(ocrPolicyRetryDelay)); err != nil && !errors.Is(err, repository.ErrOCRLeaseLost) {
				log.Printf("[file_ocr] policy_block_release_failed file_id=%d err=%v", file.ID, err)
			}
			return
		}
		r.submit(ctx, cfg, policy, file)
		return
	}
	// A remote task is already an irreversible disclosure. Continue polling it
	// even when new extraction is disabled; if the size policy is unavailable,
	// defer the local completion mutation until a trustworthy snapshot returns.
	r.poll(ctx, cfg, policy, policyErr, file)
}

func (r *OCRRecoveryRunner) submit(ctx context.Context, cfg service.MinerUOCRConfig, policy attachmentProcessingPolicy, file *model.File) {
	attempts, err := r.fileRepo.RecordOCRAttempt(file.ID, file.UserID, file.OCRLeaseGeneration)
	if err != nil {
		log.Printf("[file_ocr] attempt_record_failed file_id=%d err=%v", file.ID, err)
		return
	}
	sourcePath, err := filepolicy.ExistingPath(valueOrEmpty(file.OCRSourcePath))
	if err != nil {
		_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_source_missing", "OCR 原文件不存在，无法继续解析")
		return
	}
	content, err := os.ReadFile(sourcePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_source_missing", "OCR 原文件不存在，无法继续解析")
			return
		}
		r.scheduleOCRRetry(file, attempts, "read_source_failed", err)
		return
	}
	inspectCtx, cancel := context.WithTimeout(ctx, minerUStartTimeout(policy.TimeoutSeconds))
	info, err := r.extractorClient.InspectOCRPDF(inspectCtx, content, file.FileName)
	cancel()
	if err != nil {
		if isPermanentOCRSubmitError(err) {
			_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_submit_rejected", humanizeOCRError(err))
			return
		}
		r.scheduleOCRRetry(file, attempts, "inspect_failed", err)
		return
	}
	if info.PageCount > 200 {
		_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_page_limit_exceeded", "PDF 超过 MinerU 精准解析 200 页限制")
		return
	}
	reserved := true
	if r.quotaService != nil {
		reserved, err = r.quotaService.ReserveOCRSubmission(ctx, file.ID, file.UserID, file.OCRLeaseGeneration, info.PageCount)
	} else {
		err = r.fileRepo.MarkOCRSubmissionStarted(file.ID, file.UserID, file.OCRLeaseGeneration)
		reserved = err == nil
	}
	if err != nil {
		if _, ok := err.(*service.QuotaError); ok {
			_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_quota_exceeded", "OCR 配额已用完，请稍后再试")
			return
		}
		log.Printf("[file_ocr] submission_reservation_failed file_id=%d err=%v", file.ID, err)
		return
	}
	if !reserved {
		return
	}
	startCtx, cancel := context.WithTimeout(ctx, minerUStartTimeout(policy.TimeoutSeconds))
	start, err := r.extractorClient.StartMinerUOCR(startCtx, content, file.FileName, cfg.BaseURL, cfg.APIKey)
	cancel()
	if err != nil {
		if isPermanentOCRSubmitError(err) {
			_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_submit_rejected", humanizeOCRError(err))
			return
		}
		if isUncertainOCRSubmissionError(err) {
			_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_submission_unknown", "OCR 任务提交结果未确认，请在服务恢复后手动重试")
			return
		}
		r.scheduleOCRRetry(file, attempts, "submit_failed", err)
		return
	}
	pages := maxInt(info.PageCount, start.PageCount)
	err = r.fileRepo.StartOCRTask(file.ID, file.UserID, file.OCRLeaseGeneration, start.TaskID, pages)
	if err != nil {
		if errors.Is(err, repository.ErrOCRLeaseLost) {
			return
		}
		log.Printf("[file_ocr] submit_state_failed file_id=%d err=%v", file.ID, err)
		_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_submission_state_unknown", "OCR 任务已提交但本地确认失败，请确认服务恢复后手动重试")
		return
	}
	_ = r.fileRepo.ReleaseOCRLease(file.ID, file.UserID, file.OCRLeaseGeneration, time.Now())
	r.Wake()
}

func (r *OCRRecoveryRunner) poll(ctx context.Context, cfg service.MinerUOCRConfig, policy attachmentProcessingPolicy, policyErr error, file *model.File) {
	taskID := strings.TrimSpace(valueOrEmpty(file.OCRTaskID))
	pollCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	result, err := r.extractorClient.PollMinerUOCR(pollCtx, taskID, cfg.BaseURL, cfg.APIKey)
	cancel()
	if err != nil {
		if isTransientOCRPollError(err) {
			attempts, attemptErr := r.fileRepo.RecordOCRAttempt(file.ID, file.UserID, file.OCRLeaseGeneration)
			if attemptErr != nil {
				log.Printf("[file_ocr] poll_attempt_record_failed file_id=%d err=%v", file.ID, attemptErr)
				return
			}
			if attempts >= ocrMaxAttempts {
				_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_retry_exhausted", "OCR 服务多次重试后仍未成功，请联系管理员或删除文件后重新上传")
				log.Printf("[file_ocr] poll_retry_exhausted file_id=%d attempts=%d cause=%v", file.ID, attempts, err)
				return
			}
			_ = r.fileRepo.UpdateOCRRunning(file.ID, file.UserID, file.OCRLeaseGeneration, file.OCRProgressPages)
			_ = r.fileRepo.ReleaseOCRLease(file.ID, file.UserID, file.OCRLeaseGeneration, time.Now().Add(ocrRetryDelayFor(attempts)))
			return
		}
		_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_upstream_failed", humanizeOCRError(err))
		return
	}
	switch result.State {
	case "ready":
		if policyErr != nil || policy.MaxOutputMB <= 0 {
			_ = r.fileRepo.ReleaseOCRLease(file.ID, file.UserID, file.OCRLeaseGeneration, time.Now().Add(ocrPolicyRetryDelay))
			return
		}
		r.complete(ctx, file, result, int64(policy.MaxOutputMB)*1024*1024)
	case "failed":
		_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, result.ErrorType, minerUFailedTaskMessage(result.ErrorType))
	default:
		_ = r.fileRepo.UpdateOCRRunning(file.ID, file.UserID, file.OCRLeaseGeneration, file.OCRProgressPages)
		_ = r.fileRepo.ReleaseOCRLease(file.ID, file.UserID, file.OCRLeaseGeneration, time.Now().Add(ocrRecoveryInterval))
	}
}

func (r *OCRRecoveryRunner) complete(ctx context.Context, file *model.File, result *extractor.MinerUPollResult, maxOutputBytes int64) {
	if result == nil {
		_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_empty_result", "OCR 未返回可读文本，请重试或删除文件")
		return
	}
	if int64(len([]byte(result.Markdown))) > maxOutputBytes {
		_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_output_too_large", "OCR 解析结果过大，请上传更小的文件")
		return
	}
	if strings.TrimSpace(result.Markdown) == "" {
		_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_empty_result", "OCR 未返回可读文本，请重试或删除文件")
		return
	}
	tempPath := ocrSidecarTempPath(file.FilePath, file.OCRLeaseGeneration)
	defer func() { _ = filepolicy.Remove(tempPath) }()
	if err := writeTextFile(tempPath, result.Markdown); err != nil {
		_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_write_failed", "OCR 结果保存失败，请重试")
		return
	}
	var promotionErr error
	err := r.fileRepo.CompleteOCRClaim(ctx, file.ID, file.UserID, file.OCRLeaseGeneration, file.FilePath, result.TokenEstimate, func() error {
		temp, err := filepolicy.ExistingPath(tempPath)
		if err != nil {
			promotionErr = err
			return err
		}
		final, err := filepolicy.ManagedPath(file.FilePath)
		if err != nil {
			promotionErr = err
			return err
		}
		if filepath.Dir(temp) != filepath.Dir(final) {
			promotionErr = errors.New("OCR temp and final sidecars are on different filesystems")
			return promotionErr
		}
		promotionErr = os.Rename(temp, final)
		return promotionErr
	})
	if err != nil {
		if errors.Is(err, repository.ErrOCRLeaseLost) {
			return
		}
		if promotionErr != nil {
			_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_write_failed", "OCR 结果保存失败，请重试")
		}
		log.Printf("[file_ocr] complete_state_failed file_id=%d err=%v", file.ID, err)
		return
	}
	if sourcePath := valueOrEmpty(file.OCRSourcePath); sourcePath != "" {
		if err := removeManagedFilePaths("", nil, file.OCRSourcePath); err != nil {
			log.Printf("[file_ocr] source_cleanup_failed file_id=%d err=%v", file.ID, err)
			return
		}
		if err := r.fileRepo.ClearOCRSourcePathClaim(file.ID, file.UserID, file.OCRLeaseGeneration, sourcePath); err != nil && !errors.Is(err, repository.ErrOCRLeaseLost) {
			log.Printf("[file_ocr] source_state_cleanup_failed file_id=%d err=%v", file.ID, err)
		}
	}
}

func (r *OCRRecoveryRunner) scheduleOCRRetry(file *model.File, attempts int, stage string, err error) {
	if attempts >= ocrMaxAttempts {
		_ = r.fileRepo.FailOCRClaim(file.ID, file.UserID, file.OCRLeaseGeneration, "ocr_retry_exhausted", "OCR 服务多次重试后仍未成功，请联系管理员或删除文件后重新上传")
		log.Printf("[file_ocr] retry_exhausted file_id=%d stage=%s attempts=%d cause=%v", file.ID, stage, attempts, err)
		return
	}
	if releaseErr := r.fileRepo.ReleaseOCRLease(file.ID, file.UserID, file.OCRLeaseGeneration, time.Now().Add(ocrRetryDelayFor(attempts))); releaseErr != nil {
		log.Printf("[file_ocr] retry_schedule_failed file_id=%d stage=%s err=%v", file.ID, stage, releaseErr)
		return
	}
	log.Printf("[file_ocr] retry_scheduled file_id=%d stage=%s attempts=%d cause=%v", file.ID, stage, attempts, err)
}

func ocrSidecarTempPath(finalPath string, generation int64) string {
	return fmt.Sprintf("%s.ocr-%d.tmp", finalPath, generation)
}

func ocrRetryDelayFor(attempts int) time.Duration {
	if attempts <= 1 {
		return ocrRetryDelay
	}
	shift := min(attempts-1, 4)
	return ocrRetryDelay * time.Duration(1<<shift)
}
