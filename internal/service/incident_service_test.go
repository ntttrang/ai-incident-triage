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

// fakeRepo records calls; the repository layer owns idempotency semantics and
// is covered by its own integration suite.
type fakeRepo struct {
	upserted []*domain.Incident
	outcome  domain.UpsertOutcome
	err      error

	list   []domain.Incident
	single *domain.Incident
}

func (f *fakeRepo) UpsertFromWebhook(_ context.Context, inc *domain.Incident) (domain.UpsertOutcome, error) {
	f.upserted = append(f.upserted, inc)
	if f.err != nil {
		return "", f.err
	}
	if f.outcome == domain.UpsertCreated {
		inc.ID = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	}
	return f.outcome, nil
}

func (f *fakeRepo) List(_ context.Context, _ domain.IncidentFilter) ([]domain.Incident, error) {
	return f.list, nil
}

func (f *fakeRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.Incident, error) {
	if f.single == nil {
		return nil, domain.ErrNotFound
	}
	return f.single, nil
}

func newService(repo domain.IncidentRepository) *service.IncidentService {
	return service.NewIncidentService(repo, logger.New("error"))
}

func TestReceiveIncidentReturnsOutcomeAndKeepsStatus(t *testing.T) {
	repo := &fakeRepo{outcome: domain.UpsertCreated}
	svc := newService(repo)

	inc := &domain.Incident{Source: "jira", ExternalID: "OPS-1", Summary: "s", Status: domain.StatusReceived}
	outcome, err := svc.ReceiveIncident(context.Background(), inc)

	require.NoError(t, err)
	assert.Equal(t, domain.UpsertCreated, outcome)
	require.Len(t, repo.upserted, 1)
	assert.Equal(t, "jira", repo.upserted[0].Source)
	assert.NotEqual(t, uuid.Nil, repo.upserted[0].ID, "repo-assigned id flows back to caller")
}

func TestReceiveIncidentDuplicateIsNotAnError(t *testing.T) {
	svc := newService(&fakeRepo{outcome: domain.UpsertDuplicate})

	outcome, err := svc.ReceiveIncident(context.Background(), &domain.Incident{ExternalID: "OPS-1", Summary: "s"})
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertDuplicate, outcome)
}

func TestReceiveIncidentPropagatesRepoError(t *testing.T) {
	boom := errors.New("db down")
	svc := newService(&fakeRepo{err: boom})

	_, err := svc.ReceiveIncident(context.Background(), &domain.Incident{ExternalID: "OPS-1", Summary: "s"})
	require.ErrorIs(t, err, boom)
}

func TestListAndGetDelegate(t *testing.T) {
	repo := &fakeRepo{
		list:   []domain.Incident{{ExternalID: "OPS-1", Summary: "s"}},
		single: &domain.Incident{ExternalID: "OPS-2", Summary: "t"},
	}
	svc := newService(repo)

	got, err := svc.ListIncidents(context.Background(), domain.IncidentFilter{Status: "received"})
	require.NoError(t, err)
	assert.Len(t, got, 1)

	one, err := svc.GetIncident(context.Background(), uuid.MustParse("00000000-0000-0000-0000-000000000002"))
	require.NoError(t, err)
	assert.Equal(t, "OPS-2", one.ExternalID)

	repo.single = nil // subsequent lookups miss
	_, err = svc.GetIncident(context.Background(), uuid.New())
	assert.ErrorIs(t, err, domain.ErrNotFound)
}
