package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// ClassifyObserver receives classification telemetry. platform/metrics
// implements it; a nil observer is allowed so tests stay dependency-free.
type ClassifyObserver interface {
	ObserveClassification(source string, severity string, duration time.Duration)
}

// ClassifyService is the worker-side use case: load an incident, classify it
// (LLM with heuristic fallback), attach a runbook, persist the verdict.
type ClassifyService struct {
	classifier domain.Classifier
	kb         domain.KnowledgeBase
	repo       domain.IncidentRepository
	log        *slog.Logger
	observe    ClassifyObserver
}

// NewClassifyService returns a classify service. observer may be nil.
func NewClassifyService(classifier domain.Classifier, kb domain.KnowledgeBase, repo domain.IncidentRepository, log *slog.Logger, observer ClassifyObserver) *ClassifyService {
	return &ClassifyService{classifier: classifier, kb: kb, repo: repo, log: log, observe: observer}
}

// Classify runs one classification attempt for one incident. The verdict is
// stored with status classified; any error means the job should retry.
func (s *ClassifyService) Classify(ctx context.Context, id uuid.UUID) error {
	start := time.Now()

	inc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("load incident %s: %w", id, err)
	}

	cls, err := s.classifier.Classify(ctx, *inc)
	if err != nil {
		return fmt.Errorf("classify incident %s: %w", id, err)
	}

	// Runbook suggestion is best-effort: a KB miss degrades the suggestion,
	// not the classification.
	if s.kb != nil {
		if ref, rbErr := s.kb.RunbookForCategory(ctx, cls.Category); rbErr == nil && ref != nil {
			cls.SuggestedRunbook = ref.URL
		} else if rbErr != nil {
			s.log.WarnContext(ctx, "runbook lookup failed", "incident_id", id, "category", cls.Category, "error", rbErr)
		}
	}

	if err := s.repo.SaveClassification(ctx, id, cls); err != nil {
		return fmt.Errorf("save classification %s: %w", id, err)
	}

	if s.observe != nil {
		s.observe.ObserveClassification(cls.Source, cls.Severity, time.Since(start))
	}
	s.log.InfoContext(ctx, "incident classified",
		"incident_id", id,
		"external_id", inc.ExternalID,
		"category", cls.Category,
		"severity", cls.Severity,
		"source", cls.Source,
	)
	return nil
}

// MarkFailed records that classification exhausted its retries.
func (s *ClassifyService) MarkFailed(ctx context.Context, id uuid.UUID) error {
	return s.repo.MarkFailed(ctx, id)
}
