// Package service implements incident use cases between adapters.
package service

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// IncidentService coordinates the receive and read use cases. Receiving goes
// through the ingest port so the upsert and the classification-job enqueue
// commit atomically.
type IncidentService struct {
	ingest domain.IncidentIngest
	repo   domain.IncidentRepository
	log    *slog.Logger
}

// NewIncidentService returns a service backed by the ingest store and read
// repository.
func NewIncidentService(ingest domain.IncidentIngest, repo domain.IncidentRepository, log *slog.Logger) *IncidentService {
	return &IncidentService{ingest: ingest, repo: repo, log: log}
}

// ReceiveIncident idempotently persists an inbound webhook projection and
// leaves a classification job in flight for created/updated outcomes.
func (s *IncidentService) ReceiveIncident(ctx context.Context, inc *domain.Incident) (domain.UpsertOutcome, error) {
	outcome, err := s.ingest.Receive(ctx, inc)
	if err != nil {
		return "", err
	}
	switch outcome {
	case domain.UpsertCreated:
		s.log.InfoContext(ctx, "incident received and queued for classification",
			"incident_id", inc.ID, "external_id", inc.ExternalID, "outcome", outcome)
	case domain.UpsertUpdated:
		s.log.InfoContext(ctx, "incident updated by new delivery, re-queued for classification",
			"incident_id", inc.ID, "external_id", inc.ExternalID, "outcome", outcome)
	case domain.UpsertDuplicate:
		s.log.InfoContext(ctx, "duplicate delivery ignored", "external_id", inc.ExternalID, "delivery_id", inc.LastDeliveryID)
	}
	return outcome, nil
}

// ListIncidents returns incidents matching the filter, without raw bodies.
func (s *IncidentService) ListIncidents(ctx context.Context, filter domain.IncidentFilter) ([]domain.Incident, error) {
	return s.repo.List(ctx, filter)
}

// GetIncident returns one incident by id, without its raw body.
func (s *IncidentService) GetIncident(ctx context.Context, id uuid.UUID) (*domain.Incident, error) {
	return s.repo.GetByID(ctx, id)
}
