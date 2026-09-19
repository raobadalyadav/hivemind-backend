package main

import (
	"context"
	"time"
)

// Scheduled jobs run on their own ticker, separate from the 2s outbox
// drain loop — a slow job must never delay event publishing. Every job takes
// an injectable `now` (testable) and is safe to run on several worker
// replicas at once because each is a single claiming SQL statement
// (UPDATE...RETURNING, INSERT...ON CONFLICT DO NOTHING, or FOR UPDATE SKIP
// LOCKED), not an in-process lock.
const jobsTickInterval = time.Minute

type job struct {
	name  string
	every time.Duration
	run   func(ctx context.Context, now time.Time) (int, error)
}

func (d *deps) jobs() []job {
	return []job{
		{"complete_plans", time.Minute, d.bookingsSvc.CompleteEndedPlans},
		{"mark_no_shows", time.Minute, d.bookingsSvc.MarkNoShows},
		{"sweep_waitlist", time.Minute, d.bookingsSvc.SweepWaitlist},
		{"send_reminders", time.Minute, d.notificationsSvc.SendDueReminders},
		{"extend_series", time.Hour, d.plansSvc.ExtendAllSeries},
	}
}

// runDueJobs runs every job whose interval has elapsed since it last ran.
func (d *deps) runDueJobs(ctx context.Context, now time.Time, last map[string]time.Time) {
	for _, j := range d.jobs() {
		if now.Sub(last[j.name]) < j.every {
			continue
		}
		last[j.name] = now
		n, err := j.run(ctx, now)
		if err != nil {
			d.logger.Error("job failed", "job", j.name, "error", err)
			continue
		}
		if n > 0 {
			d.logger.Info("job done", "job", j.name, "affected", n)
		}
	}
}

func (d *deps) runJobs(ctx context.Context) {
	last := map[string]time.Time{}
	ticker := time.NewTicker(jobsTickInterval)
	defer ticker.Stop()
	d.runDueJobs(ctx, time.Now(), last)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.runDueJobs(ctx, now, last)
		}
	}
}
