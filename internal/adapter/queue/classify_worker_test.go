package queue_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/adapter/queue"
	"github.com/ntttrang/ai-incident-triage/internal/domain"
	"github.com/ntttrang/ai-incident-triage/internal/service"
)

// --- fakes wired through the real ClassifyService ---------------------------

type okClassifier struct{}

func (okClassifier) Classify(_ context.Context, _ domain.Incident) (*domain.Classification, error) {
	return &domain.Classification{Category: "access", Severity: "low", Source: domain.ClassifySourceHeuristic}, nil
}

type failClassifier struct{}

func (failClassifier) Classify(_ context.Context, _ domain.Incident) (*domain.Classification, error) {
	return nil, errors.New("every classifier is down")
}

type nilKB struct{}

func (nilKB) RunbookForCategory(context.Context, string) (*domain.RunbookRef, error) {
	return nil, nil
}

type markRepo struct {
	incident *domain.Incident
	marked   []uuid.UUID
	markErr  error
}

func (r *markRepo) List(context.Context, domain.IncidentFilter) ([]domain.Incident, error) {
	return nil, nil
}

func (r *markRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.Incident, error) {
	if r.incident == nil {
		return nil, domain.ErrNotFound
	}
	return r.incident, nil
}

func (r *markRepo) SaveClassification(context.Context, uuid.UUID, *domain.Classification) error {
	return nil
}

func (r *markRepo) MarkFailed(_ context.Context, id uuid.UUID) error {
	if r.markErr != nil {
		return r.markErr
	}
	r.marked = append(r.marked, id)
	return nil
}

func newWorker(classifier domain.Classifier, repo domain.IncidentRepository) *queue.ClassifyWorker {
	svc := service.NewClassifyService(classifier, nilKB{}, repo, slog.New(slog.DiscardHandler), nil)
	return queue.NewClassifyWorker(svc, slog.New(slog.DiscardHandler))
}

func classifyJob(attempt int) *river.Job[queue.ClassifyIncidentArgs] {
	return &river.Job[queue.ClassifyIncidentArgs]{
		JobRow: &rivertype.JobRow{Attempt: attempt, MaxAttempts: queue.MaxAttempts},
		Args:   queue.ClassifyIncidentArgs{IncidentID: uuid.MustParse("00000000-0000-0000-0000-0000000000aa")},
	}
}

func TestWorkSuccessReturnsNil(t *testing.T) {
	repo := &markRepo{incident: &domain.Incident{Summary: "s"}}
	w := newWorker(okClassifier{}, repo)

	require.NoError(t, w.Work(context.Background(), classifyJob(1)))
	assert.Empty(t, repo.marked)
}

func TestWorkErrorRetriesWithoutMarkingFailed(t *testing.T) {
	repo := &markRepo{incident: &domain.Incident{Summary: "s"}}
	w := newWorker(failClassifier{}, repo)

	err := w.Work(context.Background(), classifyJob(1))
	require.Error(t, err, "a failed attempt returns an error so River retries")
	assert.Empty(t, repo.marked, "non-terminal attempts must not mark the incident failed")
}

func TestWorkTerminalAttemptMarksIncidentFailed(t *testing.T) {
	repo := &markRepo{incident: &domain.Incident{Summary: "s"}}
	w := newWorker(failClassifier{}, repo)

	job := classifyJob(queue.MaxAttempts)
	err := w.Work(context.Background(), job)
	require.Error(t, err, "the error still surfaces; River then discards the job")
	require.Equal(t, []uuid.UUID{job.Args.IncidentID}, repo.marked)
}

// ctxCheckingMarkRepo fails MarkFailed on a cancelled context, like a real
// database write would — proving the worker detaches the mark context.
type ctxCheckingMarkRepo struct {
	markRepo
	marked []uuid.UUID
}

func (r *ctxCheckingMarkRepo) MarkFailed(ctx context.Context, id uuid.UUID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.marked = append(r.marked, id)
	return nil
}

// The terminal error is usually the JobTimeout firing, which means River has
// already cancelled the Work context. The status write must still land on its
// detached context, or the incident stays queued forever with no pending job.
func TestWorkTerminalAttemptMarksFailedOnCancelledContext(t *testing.T) {
	repo := &ctxCheckingMarkRepo{markRepo: markRepo{incident: &domain.Incident{Summary: "s"}}}
	w := newWorker(failClassifier{}, repo)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	job := classifyJob(queue.MaxAttempts)
	err := w.Work(ctx, job)
	require.Error(t, err)
	require.Equal(t, []uuid.UUID{job.Args.IncidentID}, repo.marked,
		"MarkFailed must run on a detached context after the Work context dies")
}

func TestWorkMarkFailedErrorDoesNotShadow(t *testing.T) {
	repo := &markRepo{incident: &domain.Incident{Summary: "s"}, markErr: errors.New("db gone")}
	w := newWorker(failClassifier{}, repo)

	err := w.Work(context.Background(), classifyJob(queue.MaxAttempts))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "every classifier is down", "the classification error wins over the mark error")
}

func TestWorkerTimeoutMatchesJobTimeout(t *testing.T) {
	w := newWorker(okClassifier{}, &markRepo{})
	assert.Equal(t, queue.JobTimeout, w.Timeout(classifyJob(1)))
	assert.True(t, queue.JobTimeout > 60*time.Second,
		"work budget must exceed the LLM context so the fallback can still run")
}
