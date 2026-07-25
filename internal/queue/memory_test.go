package queue

import (
	"context"
	"testing"
	"time"
)

func TestMemoryEnqueueClaim(t *testing.T) {
	q := NewMemory(2)
	job := Job{ID: "1", Prompt: "hi"}

	if err := q.Enqueue(context.Background(), job); err != nil {
		t.Fatal(err)
	}

	got, err := q.Claim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "1" || got.Prompt != "hi" {
		t.Fatalf("unexpected job: %+v", got)
	}
}

func TestMemoryClose(t *testing.T) {
	q := NewMemory(1)
	q.Close()

	err := q.Enqueue(context.Background(), Job{ID: "x"})
	if err != ErrClosed {
		t.Fatalf("expected ErrClosed, got %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = q.Claim(ctx)
	if err != ErrClosed && err != context.DeadlineExceeded {
		// closed empty channel returns ErrClosed
		if err != ErrClosed {
			t.Fatalf("unexpected claim err: %v", err)
		}
	}
}
