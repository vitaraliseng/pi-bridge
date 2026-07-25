package queue

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed is returned when the queue has been closed.
var ErrClosed = errors.New("queue closed")

// Memory is a simple bounded in-memory job queue backed by a channel.
type Memory struct {
	ch     chan Job
	closed bool
	mu     sync.RWMutex
}

// NewMemory creates a queue with the given capacity.
func NewMemory(capacity int) *Memory {
	if capacity < 1 {
		capacity = 64
	}
	return &Memory{ch: make(chan Job, capacity)}
}

// Enqueue adds a job. Blocks if the queue is full until ctx is done.
func (q *Memory) Enqueue(ctx context.Context, job Job) error {
	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		return ErrClosed
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case q.ch <- job:
		return nil
	}
}

// Claim waits for the next job.
func (q *Memory) Claim(ctx context.Context) (Job, error) {
	select {
	case <-ctx.Done():
		return Job{}, ctx.Err()
	case job, ok := <-q.ch:
		if !ok {
			return Job{}, ErrClosed
		}
		return job, nil
	}
}

// Close prevents new enqueues and unblocks Claim callers after drain.
func (q *Memory) Close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.closed = true
	close(q.ch)
}
