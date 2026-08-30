package domain

import (
	"time"

	"github.com/google/uuid"
)

// IncidentStatus tracks an incident through the triage pipeline.
type IncidentStatus string

const (
	// StatusReceived predates transactional enqueue: rows now start life as
	// StatusQueued. Retained for read compatibility with pre-phase-3 data.
	StatusReceived IncidentStatus = "received"
	// StatusQueued is set when classification is enqueued (Phase 3).
	StatusQueued IncidentStatus = "queued"
	// StatusClassified is set when the LLM worker stored a result.
	StatusClassified IncidentStatus = "classified"
	// StatusFailed is set when classification exhausted its retries.
	StatusFailed IncidentStatus = "failed"
)

// UpsertOutcome reports what an idempotent webhook upsert did.
type UpsertOutcome string

const (
	// UpsertCreated means a new incident row was inserted.
	UpsertCreated UpsertOutcome = "created"
	// UpsertUpdated means an existing issue received a new delivery and its
	// payload was refreshed.
	UpsertUpdated UpsertOutcome = "updated"
	// UpsertDuplicate means the delivery was already processed; nothing was
	// written.
	UpsertDuplicate UpsertOutcome = "duplicate"
)

// Incident is the aggregate received from an issue tracker webhook and later
// enriched by the classification worker. Classification fields stay nil until
// the worker populates them.
type Incident struct {
	ID         uuid.UUID
	Source     string // originating system, always "jira" today
	ExternalID string // issue key, e.g. OPS-42
	Summary    string
	// Description is a plain-text projection of the (possibly ADF) issue
	// description; the verbatim payload lives in Raw.
	Description    string
	IssueType      string
	Priority       string
	Labels         []string
	Reporter       string
	Status         IncidentStatus
	Raw            []byte  // full webhook body, stored for debugging; never returned by the read API
	LastDeliveryID *string // X-Atlassian-Webhook-Identifier; nil when the header is absent

	// Classification result (Phase 3). Reset to nil whenever a new delivery
	// re-enqueues classification.
	Category             *string
	Severity             *string
	PriorityScore        *int
	Confidence           *float64
	Rationale            *string
	SuggestedRunbook     *string
	ClassificationSource *string
	ClassifiedAt         *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// KnownStatus reports whether s is one of the defined pipeline statuses.
func KnownStatus(s IncidentStatus) bool {
	switch s {
	case StatusReceived, StatusQueued, StatusClassified, StatusFailed:
		return true
	}
	return false
}

// IncidentFilter narrows List results; empty fields are not applied.
type IncidentFilter struct {
	Status   string
	Severity string
	Limit    int
	Offset   int
}
