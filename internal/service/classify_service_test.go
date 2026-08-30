package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
	"github.com/ntttrang/ai-incident-triage/internal/platform/logger"
	"github.com/ntttrang/ai-incident-triage/internal/service"
)

// --- classification-path fakes -------------------------------------------

type fakeClassifier struct {
	cls *domain.Classification
	err error
}

func (f fakeClassifier) Classify(context.Context, domain.Incident) (*domain.Classification, error) {
	return f.cls, f.err
}

type fakeKB struct {
	ref *domain.RunbookRef
	err error
}

func (f fakeKB) RunbookForCategory(_ context.Context, _ string) (*domain.RunbookRef, error) {
	return f.ref, f.err
}

type classRepo struct {
	single  *domain.Incident
	getErr  error
	saved   map[uuid.UUID]*domain.Classification
	marked  []uuid.UUID
	saveErr error
}

func newClassRepo(single *domain.Incident) *classRepo {
	return &classRepo{single: single, saved: map[uuid.UUID]*domain.Classification{}}
}

func (r *classRepo) List(context.Context, domain.IncidentFilter) ([]domain.Incident, error) {
	return nil, nil
}

func (r *classRepo) GetByID(context.Context, uuid.UUID) (*domain.Incident, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.single, nil
}

func (r *classRepo) SaveClassification(_ context.Context, id uuid.UUID, cls *domain.Classification) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saved[id] = cls
	return nil
}

func (r *classRepo) MarkFailed(_ context.Context, id uuid.UUID) error {
	r.marked = append(r.marked, id)
	return nil
}

// Compile-time proof the fake satisfies the worker-side repository port.
var _ domain.IncidentRepository = (*classRepo)(nil)

type countingObserver struct {
	calls    int
	source   string
	severity string
}

func (o *countingObserver) ObserveClassification(source, severity string, _ time.Duration) {
	o.calls++
	o.source = source
	o.severity = severity
}

// --- tests ----------------------------------------------------------------

func TestClassifyStoresVerdictWithRunbook(t *testing.T) {
	id := uuid.New()
	repo := newClassRepo(&domain.Incident{ID: id, ExternalID: "OPS-1", Summary: "db down"})
	verdict := &domain.Classification{Category: "database", Severity: "critical", Source: domain.ClassifySourceLLM}
	obs := &countingObserver{}

	svc := service.NewClassifyService(
		fakeClassifier{cls: verdict},
		fakeKB{ref: &domain.RunbookRef{URL: "https://runbooks.example.com/database"}},
		repo, logger.New("error"), obs,
	)
	require.NoError(t, svc.Classify(context.Background(), id))

	saved := repo.saved[id]
	require.NotNil(t, saved)
	assert.Equal(t, "database", saved.Category)
	assert.Equal(t, "https://runbooks.example.com/database", saved.SuggestedRunbook, "runbook attaches best-effort")
	require.Equal(t, 1, obs.calls)
	assert.Equal(t, domain.ClassifySourceLLM, obs.source)
	assert.Equal(t, "critical", obs.severity)
}

func TestClassifySurvivesKnowledgeBaseFailure(t *testing.T) {
	id := uuid.New()
	repo := newClassRepo(&domain.Incident{ID: id, Summary: "s"})

	svc := service.NewClassifyService(
		fakeClassifier{cls: &domain.Classification{Category: "access", Severity: "low", Source: domain.ClassifySourceHeuristic}},
		fakeKB{err: errors.New("notion down")},
		repo, logger.New("error"), nil,
	)
	require.NoError(t, svc.Classify(context.Background(), id))

	saved := repo.saved[id]
	require.NotNil(t, saved, "classification still persists without the KB")
	assert.Empty(t, saved.SuggestedRunbook)
}

func TestClassifyPropagatesClassifierError(t *testing.T) {
	id := uuid.New()
	repo := newClassRepo(&domain.Incident{ID: id, Summary: "s"})
	boom := errors.New("all classifiers failed")

	svc := service.NewClassifyService(fakeClassifier{err: boom}, fakeKB{}, repo, logger.New("error"), nil)
	err := svc.Classify(context.Background(), id)
	require.ErrorIs(t, err, boom)
	assert.Empty(t, repo.saved, "nothing persists when classification fails")
}

func TestClassifyMissingIncidentErrors(t *testing.T) {
	repo := newClassRepo(nil)
	repo.getErr = domain.ErrNotFound

	svc := service.NewClassifyService(fakeClassifier{}, fakeKB{}, repo, logger.New("error"), nil)
	err := svc.Classify(context.Background(), uuid.New())
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestMarkFailedDelegates(t *testing.T) {
	repo := newClassRepo(nil)
	svc := service.NewClassifyService(fakeClassifier{}, fakeKB{}, repo, logger.New("error"), nil)

	id := uuid.New()
	require.NoError(t, svc.MarkFailed(context.Background(), id))
	require.Equal(t, []uuid.UUID{id}, repo.marked)
}
