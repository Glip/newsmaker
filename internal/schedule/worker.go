package schedule

import (
	"context"
	"log"
	"time"
)

// Runner fires due posts via Execute.
type Runner struct {
	Store    *Store
	Execute  func(ctx context.Context, p Post) (jobID string, err error)
	Interval time.Duration
	Log      *log.Logger
}

func (r *Runner) Start(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	logger := r.Log
	if logger == nil {
		logger = log.Default()
	}
	// Recover interrupted sends after restart.
	if n, err := r.Store.RecoverStuckSending(); err != nil {
		logger.Printf("schedule recover: %v", err)
	} else if n > 0 {
		logger.Printf("schedule: re-queued %d stuck sending posts", n)
	}

	t := time.NewTicker(interval)
	go func() {
		defer t.Stop()
		r.tick(ctx, logger)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.tick(ctx, logger)
			}
		}
	}()
}

func (r *Runner) tick(ctx context.Context, logger *log.Logger) {
	due, err := r.Store.ListDue(time.Now().UTC(), 10)
	if err != nil {
		logger.Printf("schedule list due: %v", err)
		return
	}
	for _, p := range due {
		if ctx.Err() != nil {
			return
		}
		ok, err := r.Store.Claim(p.ID)
		if err != nil {
			logger.Printf("schedule claim %s: %v", p.ID, err)
			continue
		}
		if !ok {
			continue
		}
		jobID, err := r.Execute(ctx, p)
		if err != nil {
			logger.Printf("schedule send %s: %v", p.ID, err)
			_ = r.Store.MarkFailed(p.ID, err.Error())
			continue
		}
		if err := r.Store.MarkDone(p.ID, jobID); err != nil {
			logger.Printf("schedule mark done %s: %v", p.ID, err)
		} else {
			logger.Printf("schedule sent %s → job %s", p.ID, jobID)
		}
	}
}
