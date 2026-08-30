package domain

import (
	"context"

	"github.com/google/uuid"
)

// IncidentIngest receives webhook projections atomically: the incident upsert
// and the classification-job enqueue happen in one transaction, or not at all.
type IncidentIngest interface {
	// Receive inserts a new incident, refreshes an existing issue when the
	// delivery identifier is new, or reports UpsertDuplicate when the delivery
	// was already processed (no write, no job). Created and updated outcomes
	// leave the row queued with a classification job enqueued in the same tx.
	Receive(ctx context.Context, inc *Incident) (UpsertOutcome, error)
}

// IncidentRepository reads incidents and persists classification results.
type IncidentRepository interface {
	// List returns incidents matching the filter, newest first, without Raw.
	List(ctx context.Context, filter IncidentFilter) ([]Incident, error)
	// GetByID returns one incident without Raw.
	GetByID(ctx context.Context, id uuid.UUID) (*Incident, error)
	// SaveClassification stores a verdict and marks the incident classified.
	SaveClassification(ctx context.Context, id uuid.UUID, cls *Classification) error
	// MarkFailed records that classification exhausted its retries.
	MarkFailed(ctx context.Context, id uuid.UUID) error
}

// Classifier grades incidents. The OpenAI adapter implements it in Phase 3.
type Classifier interface {
	Classify(ctx context.Context, inc Incident) (*Classification, error)
}

// RunbookRef is a knowledge-base document relevant to an incident.
type RunbookRef struct {
	Title string
	URL   string
}

// KnowledgeBase resolves a runbook for a classified category. The static
// stub implements it now; the Notion RAG adapter implements it in a later
// phase behind this same contract.
type KnowledgeBase interface {
	RunbookForCategory(ctx context.Context, category string) (*RunbookRef, error)
}
