package domain

import (
	"context"

	"github.com/google/uuid"
)

// IncidentRepository persists incidents and resolves webhook idempotency.
type IncidentRepository interface {
	// UpsertFromWebhook inserts a new incident, refreshes an existing issue
	// when the delivery identifier is new, or reports UpsertDuplicate when the
	// delivery was already processed (no write).
	UpsertFromWebhook(ctx context.Context, inc *Incident) (UpsertOutcome, error)
	// List returns incidents matching the filter, newest first, without Raw.
	List(ctx context.Context, filter IncidentFilter) ([]Incident, error)
	// GetByID returns one incident without Raw.
	GetByID(ctx context.Context, id uuid.UUID) (*Incident, error)
}

// Classification is the LLM verdict for one incident (populated in Phase 3).
type Classification struct {
	Category         string
	Severity         string
	PriorityScore    int
	Confidence       float64
	Rationale        string
	SuggestedRunbook string
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

// KnowledgeBase retrieves relevant runbooks for an incident. The Notion RAG
// adapter implements it in a later phase; the contract is declared here so the
// domain owns it.
type KnowledgeBase interface {
	RetrieveRunbooks(ctx context.Context, inc Incident) ([]RunbookRef, error)
}
