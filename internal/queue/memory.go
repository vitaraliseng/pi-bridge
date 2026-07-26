package queue

import (
	"context"
	"errors"
	"sync"
)

// ErrClosed is returned when the queue has been closed.
var ErrClosed = errors.New("queue closed")

// Memory is a simple bounded in-memory job queue backed by a channel.
// Close signals via done and never closes ch, so Enqueue cannot panic on send.
type Memory struct {
	ch     chan Job
	done   chan struct{}
	closed bool
	mu     sync.Mutex
}

// NewMemory creates a queue with the given capacity.
func NewMemory(capacity int) *Memory {
	if capacity < 1 {
		capacity = 64
	}
	return &Memory{
		ch:   make(chan Job, capacity),
		done: make(chan struct{}),
	}
}

// Enqueue adds a job. Blocks if the queue is full until ctx is done or Close.
func (q *Memory) Enqueue(ctx context.Context, job Job) error {
	// Fast path: reject or non-blocking send under lock so Close can't race a send.
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return ErrClosed
	}
	select {
	case q.ch <- job:
		q.mu.Unlock()
		return nil
	default:
		q.mu.Unlock()
	}

	// Full: wait without holding mu (Close must not deadlock).
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-q.done:
		return ErrClosed
	case q.ch <- job:
		return nil
	}
}

// Claim waits for the next job.
func (q *Memory) Claim(ctx context.Context) (Job, error) {
	for {
		select {
		case <-ctx.Done():
			return Job{}, ctx.Err()
		case job := <-q.ch:
			return job, nil
		case <-q.done:
			// Drain any jobs still buffered, then stop.
			select {
			case job := <-q.ch:
				return job, nil
			default:
				return Job{}, ErrClosed
			}
		}
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
	close(q.done)
}
