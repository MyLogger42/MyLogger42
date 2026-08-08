// Package retention implements the background log purge worker.
//
// On a 1-hour ticker the worker iterates every project (including
// archived ones — archived projects don't accept new ingest but their
// existing rows still consume disk and are still subject to retention)
// and applies the effective window:
//
//   p.RetentionDays != nil  → *p.RetentionDays   (0 = retain forever)
//   p.RetentionDays == nil  → globalDefault       (0 = retain forever)
//
// A failure on one project doesn't abort the sweep; the next project
// gets its turn.
package retention

import (
	"context"
	"log"
	"time"

	"github.com/mylogger42/mylogger42/internal/domain"
	"github.com/mylogger42/mylogger42/internal/store"
)

// tickInterval is how often we wake up to enforce retention. Per spec,
// hourly is plenty — the cutoff window is measured in days.
const tickInterval = time.Hour

// Worker enforces the retention window on a recurring schedule.
type Worker struct {
	logs           store.LogStore
	projects       store.ProjectStore
	globalDefault  int
	logger         *log.Logger
	now            func() time.Time
}

// New constructs a Worker.
//
// globalDefault is the fallback retention window for projects whose
// own RetentionDays is nil (inherit). Set to 0 to disable
// inheritance — projects then retain forever unless they set their
// own positive RetentionDays.
func New(logs store.LogStore, projects store.ProjectStore, globalDefault int, logger *log.Logger) *Worker {
	if logger == nil {
		logger = log.Default()
	}
	return &Worker{
		logs:          logs,
		projects:      projects,
		globalDefault: globalDefault,
		logger:        logger,
		now:           time.Now,
	}
}

// Run blocks until ctx is cancelled. Intended to be launched in its own
// goroutine at startup.
func (w *Worker) Run(ctx context.Context) {
	w.logger.Printf("retention: starting (global default = %d days, tick = %s)",
		w.globalDefault, tickInterval)

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

// sweep walks every project and applies its effective retention. We
// include archived projects — archive doesn't pause retention. An
// operator who wants indefinite retention on an archived project must
// set retention_days = 0 BEFORE archiving (per the spec's
// "operators must set retention_days=0 before archiving" note).
func (w *Worker) sweep(ctx context.Context) {
	projects, err := w.projects.List(ctx, true /* includeArchived */)
	if err != nil {
		w.logger.Printf("retention: list projects: %v", err)
		return
	}
	if len(projects) == 0 {
		return
	}
	for _, p := range projects {
		days := w.effectiveRetention(p)
		if days <= 0 {
			continue // retain forever for this project
		}
		cutoff := w.now().Add(-time.Duration(days) * 24 * time.Hour)
		deleted, err := w.logs.DeleteOlderThanForProject(ctx, p.ID, cutoff)
		if err != nil {
			w.logger.Printf("retention: project=%s sweep failed: %v", p.Slug, err)
			continue
		}
		if deleted > 0 {
			w.logger.Printf("retention: project=%s deleted=%d cutoff=%s",
				p.Slug, deleted, cutoff.Format(time.RFC3339))
		}
	}
}

// effectiveRetention computes the retention window for a single project.
// Returns days (0 = retain forever).
func (w *Worker) effectiveRetention(p domain.Project) int {
	if p.RetentionDays != nil {
		return *p.RetentionDays
	}
	return w.globalDefault
}
