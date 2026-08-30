package queue

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"

	"github.com/ntttrang/ai-incident-triage/internal/service"
)

// ClassifyIncidentArgs carries the only state a classification job needs.
// Args are stored as JSONB, so fields must have json tags. Kind is the durable
// job identifier: renaming it orphans in-flight jobs.
type ClassifyIncidentArgs struct {
	IncidentID uuid.UUID `json:"incident_id"`
}

// Kind identifies the job type in river_job.kind.
func (ClassifyIncidentArgs) Kind() string { return "classify_incident" }

// ClassifyWorker runs one classification attempt for one incident.
type ClassifyWorker struct {
	river.WorkerDefaults[ClassifyIncidentArgs]
	svc *service.ClassifyService
	log *slog.Logger
}

// NewClassifyWorker returns a worker backed by the classify service.
func NewClassifyWorker(svc *service.ClassifyService, log *slog.Logger) *ClassifyWorker {
	return &ClassifyWorker{svc: svc, log: log}
}

// Timeout keeps the work budget (JobTimeout) wider than the LLM context so the
// heuristic fallback always gets a chance to run and persist.
func (w *ClassifyWorker) Timeout(*river.Job[ClassifyIncidentArgs]) time.Duration {
	return JobTimeout
}

// Work classifies the incident. A returned error makes River retry with
// backoff; when this is the final attempt the incident is marked failed
// before the error surfaces (the job then becomes discarded).
func (w *ClassifyWorker) Work(ctx context.Context, job *river.Job[ClassifyIncidentArgs]) error {
	id := job.Args.IncidentID
	err := w.svc.Classify(ctx, id)
	if err == nil {
		return nil
	}

	if job.Attempt >= job.MaxAttempts {
		w.log.ErrorContext(ctx, "classification exhausted retries, marking incident failed",
			"incident_id", id, "attempt", job.Attempt, "max_attempts", job.MaxAttempts, "error", err)
		// The terminal error is often the JobTimeout firing, which means ctx
		// is already cancelled here. The status write needs a detached
		// context, or the incident would stay queued forever with no pending
		// job — invisible to both the retry loop and the failure state.
		markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if markErr := w.svc.MarkFailed(markCtx, id); markErr != nil {
			// Log but do not shadow the classification error; the incident
			// stays queued and the phase-6 runbook reconciliation query
			// catches it.
			w.log.ErrorContext(ctx, "mark incident failed", "incident_id", id, "error", markErr)
		}
	} else {
		w.log.WarnContext(ctx, "classification attempt failed, will retry",
			"incident_id", id, "attempt", job.Attempt, "max_attempts", job.MaxAttempts, "error", err)
	}
	return err
}
