package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
	"github.com/ntttrang/ai-incident-triage/internal/platform/logger"
	"github.com/ntttrang/ai-incident-triage/internal/service"
)

// fakeStore stands in for the ingest store (Receive) and repository (reads).
// Idempotency semantics live in the postgres layer and are covered by its own
// integration suite.
type fakeStore struct {
	received []*domain.Incident
	outcome  domain.UpsertOutcome
	err      error

	list   []domain.Incident
	single *domain.Incident
}

func (f *fakeStore) Receive(_ context.Context, inc *domain.Incident) (domain.UpsertOutcome, error) {
	f.received = append(f.received, inc)
	if f.err != nil {
		return "", f.err
	}
	if f.outcome == domain.UpsertCreated {
		inc.ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	}
	return f.outcome, nil
}

func (f *fakeStore) List(_ context.Context, _ domain.IncidentFilter) ([]domain.Incident, error) {
	return f.list, nil
}

func (f *fakeStore) GetByID(_ context.Context, _ uuid.UUID) (*domain.Incident, error) {
	if f.single == nil {
		return nil, domain.ErrNotFound
	}
	return f.single, nil
}

func (f *fakeStore) SaveClassification(context.Context, uuid.UUID, *domain.Classification) error {
	return nil
}

func (f *fakeStore) MarkFailed(context.Context, uuid.UUID) error { return nil }

func newService(store *fakeStore) *service.IncidentService {
	return service.NewIncidentService(store, store, logger.New("error"))
}

func TestReceiveIncidentReturnsOutcomeAndKeepsStatus(t *testing.T) {
	store := &fakeStore{outcome: domain.UpsertCreated}
	svc := newService(store)

	inc := &domain.Incident{Source: "jira", ExternalID: "OPS-1", Summary: "s", Status: domain.StatusReceived}
	outcome, err := svc.ReceiveIncident(context.Background(), inc)

	require.NoError(t, err)
	assert.Equal(t, domain.UpsertCreated, outcome)
	require.Len(t, store.received, 1)
	assert.Equal(t, "jira", store.received[0].Source)
	assert.NotEqual(t, uuid.Nil, store.received[0].ID, "store-assigned id flows back to caller")
}

func TestReceiveIncidentDuplicateIsNotAnError(t *testing.T) {
	svc := newService(&fakeStore{outcome: domain.UpsertDuplicate})

	outcome, err := svc.ReceiveIncident(context.Background(), &domain.Incident{ExternalID: "OPS-1", Summary: "s"})
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertDuplicate, outcome)
}

func TestReceiveIncidentPropagatesStoreError(t *testing.T) {
	boom := errors.New("db down")
	svc := newService(&fakeStore{err: boom})

	_, err := svc.ReceiveIncident(context.Background(), &domain.Incident{ExternalID: "OPS-1", Summary: "s"})
	require.ErrorIs(t, err, boom)
}

func TestListAndGetDelegate(t *testing.T) {
	store := &fakeStore{
		list:   []domain.Incident{{ExternalID: "OPS-1", Summary: "s"}},
		single: &domain.Incident{ExternalID: "OPS-2", Summary: "t"},
	}
	svc := newService(store)

	got, err := svc.ListIncidents(context.Background(), domain.IncidentFilter{Status: "queued"})
	require.NoError(t, err)
	assert.Len(t, got, 1)

	one, err := svc.GetIncident(context.Background(), uuid.MustParse("00000000-0000-0000-0000-000000000002"))
	require.NoError(t, err)
	assert.Equal(t, "OPS-2", one.ExternalID)

	store.single = nil // subsequent lookups miss
	_, err = svc.GetIncident(context.Background(), uuid.New())
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

// Compile-time proof that the fake satisfies both ports the service consumes.
var (
	_ domain.IncidentIngest     = (*fakeStore)(nil)
	_ domain.IncidentRepository = (*fakeStore)(nil)
)
