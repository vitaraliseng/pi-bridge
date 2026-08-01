package worker

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestAnnotateTimeout(t *testing.T) {
	t.Parallel()

	err := annotateTimeout(context.DeadlineExceeded, 10*time.Minute)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected wrapped deadline, got %v", err)
	}
	if !strings.Contains(err.Error(), "job timed out after 10m0s") {
		t.Fatalf("expected timeout duration in message, got %v", err)
	}
	if !strings.Contains(err.Error(), "job_timeout") {
		t.Fatalf("expected config hint in message, got %v", err)
	}

	other := errors.New("boom")
	if got := annotateTimeout(other, time.Minute); got != other {
		t.Fatalf("non-timeout error should pass through, got %v", got)
	}
}
