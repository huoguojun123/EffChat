package service

import (
	"context"
	"errors"
	"testing"

	"github.com/huoguojun123/EffChat/internal/modelstream"
)

func TestRunCancellationErrorPreservesStandardErrorFamily(t *testing.T) {
	firstOutputErr := runCancellationError{Cause: RunCancelFirstOutputTimeout}
	if !errors.Is(firstOutputErr, modelstream.ErrFirstOutputTimeout) {
		t.Fatalf("first-output cancellation does not unwrap to ErrFirstOutputTimeout: %v", firstOutputErr)
	}
	if !errors.Is(firstOutputErr, context.DeadlineExceeded) {
		t.Fatalf("first-output cancellation does not unwrap to DeadlineExceeded: %v", firstOutputErr)
	}

	for _, cause := range []RunCancelCause{
		RunCancelUserStop,
		RunCancelServerDrain,
		RunCancelAccountChanged,
		RunCancelSessionDeleted,
		RunCancelUpstream,
	} {
		t.Run(string(cause), func(t *testing.T) {
			if err := (runCancellationError{Cause: cause}); !errors.Is(err, context.Canceled) {
				t.Fatalf("%q does not unwrap to context.Canceled: %v", cause, err)
			}
		})
	}
}
