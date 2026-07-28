package handler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestMinerUStartTimeoutHasLongUploadFloor(t *testing.T) {
	if got := minerUStartTimeout(60); got != 5*time.Minute {
		t.Fatalf("minerUStartTimeout(60) = %s, want 5m", got)
	}
	if got := minerUStartTimeout(600); got != 10*time.Minute {
		t.Fatalf("minerUStartTimeout(600) = %s, want 10m", got)
	}
}

func TestHumanizeOCRErrorDoesNotExposeRawUpstreamError(t *testing.T) {
	raw := `python extractor mineru poll failed: Get "http://py-extractor:8090/ocr/mineru/tasks/task-1?token=secret": context deadline exceeded`
	got := humanizeOCRError(errors.New(raw))
	if strings.Contains(got, "py-extractor") || strings.Contains(got, "token=secret") || strings.Contains(got, "context deadline exceeded") {
		t.Fatalf("humanizeOCRError leaked raw error: %q", got)
	}
	if got != "OCR 解析启动失败，请稍后重试或删除文件后重新上传" {
		t.Fatalf("humanizeOCRError = %q", got)
	}
}

func TestOCRFailureMessagesStayUserSafe(t *testing.T) {
	if got := minerUFailedTaskMessage(`mineru_failed: token=secret py-extractor`); strings.Contains(got, "secret") || strings.Contains(got, "py-extractor") {
		t.Fatalf("minerUFailedTaskMessage leaked raw detail: %q", got)
	}
	got := combineOCRMessages("OCR 解析启动失败，请稍后重试或删除文件后重新上传", "本地解析兜底也失败，请删除后重新上传")
	if strings.Contains(got, "python extractor") || strings.Contains(got, "token=") {
		t.Fatalf("combineOCRMessages leaked raw detail: %q", got)
	}
	if !strings.Contains(got, "本地解析兜底也失败") {
		t.Fatalf("combineOCRMessages lost fallback action hint: %q", got)
	}
}

func TestOCRSubmitErrorClassificationAndBackoff(t *testing.T) {
	for _, tc := range []struct {
		err       error
		permanent bool
	}{
		{errors.New("python extractor mineru start returned 401: unauthorized"), true},
		{errors.New("python extractor mineru start returned 400: invalid request"), true},
		{errors.New("python extractor mineru start returned 429: rate limited"), false},
		{errors.New("python extractor mineru start returned 503: unavailable"), false},
		{context.DeadlineExceeded, false},
	} {
		if got := isPermanentOCRSubmitError(tc.err); got != tc.permanent {
			t.Fatalf("isPermanentOCRSubmitError(%q)=%v, want %v", tc.err, got, tc.permanent)
		}
	}
	if got := ocrRetryDelayFor(1); got != 15*time.Second {
		t.Fatalf("first retry delay=%s", got)
	}
	if got := ocrRetryDelayFor(5); got != 4*time.Minute {
		t.Fatalf("fifth retry delay=%s", got)
	}
}

func TestUncertainOCRSubmissionErrorsDoNotRetry(t *testing.T) {
	for _, err := range []error{
		context.DeadlineExceeded,
		errors.New("connection reset by peer"),
		errors.New("unexpected EOF"),
		errors.New("decode mineru start response: unexpected end of JSON input"),
		errors.New("mineru start returned no task id"),
	} {
		if !isUncertainOCRSubmissionError(err) {
			t.Fatalf("isUncertainOCRSubmissionError(%q)=false", err)
		}
	}
}
