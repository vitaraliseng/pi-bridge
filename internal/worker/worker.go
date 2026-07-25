package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/vitaraliseng/pi-bridge/internal/pi"
	"github.com/vitaraliseng/pi-bridge/internal/queue"
)

// Sink receives job progress and final results.
type Sink interface {
	OnStart(ctx context.Context, job queue.Job) error
	OnProgress(ctx context.Context, job queue.Job, p pi.Progress)
	OnComplete(ctx context.Context, job queue.Job, text string) error
	OnError(ctx context.Context, job queue.Job, err error)
}

// Worker pulls jobs from a queue and runs them through pi.
type Worker struct {
	ID      string
	Queue   *queue.Memory
	Pool    *pi.Pool
	Sink    Sink
	Timeout time.Duration
	Log     *slog.Logger
}

// Run blocks until ctx is canceled or the queue closes.
func (w *Worker) Run(ctx context.Context) error {
	if w.Log == nil {
		w.Log = slog.Default()
	}
	if w.Timeout == 0 {
		w.Timeout = 10 * time.Minute
	}

	for {
		job, err := w.Queue.Claim(ctx)
		if err != nil {
			return err
		}
		w.handle(ctx, job)
	}
}

func (w *Worker) handle(parent context.Context, job queue.Job) {
	log := w.Log.With(
		"worker", w.ID,
		"job", job.ID,
		"session", job.SessionKey,
	)
	log.Info("claimed job")

	ctx, cancel := context.WithTimeout(parent, w.Timeout)
	defer cancel()

	if err := w.Sink.OnStart(ctx, job); err != nil {
		log.Error("sink start failed", "err", err)
	}

	client, err := w.Pool.Acquire(ctx, job.SessionKey, job.CWD)
	if err != nil {
		log.Error("acquire pi session failed", "err", err)
		w.Sink.OnError(ctx, job, err)
		return
	}

	text, err := client.RunPrompt(ctx, job.Prompt, func(p pi.Progress) {
		w.Sink.OnProgress(ctx, job, p)
	})
	if err != nil {
		log.Error("prompt failed", "err", err)
		w.Sink.OnError(ctx, job, err)
		return
	}

	if err := w.Sink.OnComplete(ctx, job, text); err != nil {
		log.Error("sink complete failed", "err", err)
	}
	log.Info("job complete", "chars", len(text))
}
