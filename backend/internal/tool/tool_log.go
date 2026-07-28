package tool

import (
	"context"
	"time"
	"unicode/utf8"
)

func webErrorCode(ctx context.Context, err error) string {
	if err == context.DeadlineExceeded || (ctx != nil && ctx.Err() == context.DeadlineExceeded) {
		return WebErrorCodeTimeout
	}
	return WebErrorCodeUnavailable
}

func webErrorMessage(ctx context.Context, err error) string {
	if webErrorCode(ctx, err) == WebErrorCodeTimeout {
		return "联网服务响应超时，请稍后重试"
	}
	return "联网服务暂时不可用，请稍后重试"
}

func toolLogDurationMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

func toolLogRuneCount(value string) int {
	return utf8.RuneCountInString(value)
}
