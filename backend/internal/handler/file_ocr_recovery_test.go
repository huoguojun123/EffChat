package handler

import (
	"context"
	"testing"
	"time"
)

func TestOCRRecoveryRunnerDrainWaitsForWorkers(t *testing.T) {
	runner := NewOCRRecoveryRunner(nil, nil, nil, nil, nil)
	workerStarted := make(chan struct{})
	releaseWorker := make(chan struct{})
	runner.startWorker(func() {
		close(workerStarted)
		<-releaseWorker
	})
	<-workerStarted

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if runner.Drain(ctx) {
		t.Fatal("drain returned before the OCR worker exited")
	}

	close(releaseWorker)
	if !runner.Drain(context.Background()) {
		t.Fatal("drain did not complete after the OCR worker exited")
	}
}

func TestOCRRecoveryRunnerDrainWithoutStartCompletes(t *testing.T) {
	runner := NewOCRRecoveryRunner(nil, nil, nil, nil, nil)
	if !runner.Drain(context.Background()) {
		t.Fatal("drain without a started loop should complete")
	}
}
