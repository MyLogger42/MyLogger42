// Package retention implements the background log purge worker.
//
// On a 1-hour ticker the worker calls store.DeleteOlderThan(now - retention)
// to enforce the configured retention window. RETENTION_DAYS=0 disables
// the worker (it logs and returns immediately).
package retention

import (
	"context"
	"log"
	"time"

	"github.com/silverbaq/mylogger42/internal/store"
)

// tickInterval is how often we wake up to enforce retention. Per spec,
// hourly is plenty — the cutoff window is measured in days.
const tickInterval = time.Hour

// Worker enforces the retention window on a recurring schedule.
type Worker struct {
	store         store.LogStore
	retentionDays int
	logger        *log.Logger
	now           func() time.Time
}

// New constructs a Worker. days==0 means retention is disabled (the
// returned worker's Run is a no-op besides logging that fact).
func New(s store.LogStore, days int, logger *log.Logger) *Worker {
	if logger == nil {
		logger = log.Default()
	}
	return &Worker{
		store:         s,
		retentionDays: days,
		logger:        logger,
		now:           time.Now,
	}
}

// Run blocks until ctx is cancelled. Intended to be launched in its own
// goroutine at startup.
func (w *Worker) Run(ctx context.Context) {
	if w.retentionDays <= 0 {
		w.logger.Printf("retention: disabled (RETENTION_DAYS=%d)", w.retentionDays)
		<-ctx.Done()
		return
	}
	w.logger.Printf("retention: enforcing window of %d days, every %s", w.retentionDays, tickInterval)

	// Run once immediately on boot so we don't sit idle for an hour
	// before the first sweep.
	w.sweep(ctx)

	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.sweep(ctx)
		}
	}
}

func (w *Worker) sweep(ctx context.Context) {
	// TODO(v2 Phase 11): iterate ProjectStore.List(includeArchived=true),
	// compute effectiveRetention(p) per project, and call
	// DeleteOlderThanForProject for each. The current body is intentionally
	// a no-op-with-warning so callers can wire up the worker without a
	// project store yet.
	w.logger.Printf("retention: sweep stubbed (Phase 11 will iterate projects); cutoff would be %s",
		w.now().Add(-time.Duration(w.retentionDays)*24*time.Hour).Format(time.RFC3339))
	_ = ctx
}
