package httpadapter

import (
	"time"

	"github.com/google/uuid"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// ErrorResponse is a standard API error body.
type ErrorResponse struct {
	Error string `json:"error" example:"invalid input"`
}

// HealthResponse is returned by health endpoints.
type HealthResponse struct {
	Status string `json:"status" example:"ok"`
}

// WebhookResponse reports what an idempotent webhook delivery did.
type WebhookResponse struct {
	Outcome string     `json:"outcome" example:"created"`
	ID      *uuid.UUID `json:"id,omitempty"`
	Status  string     `json:"status,omitempty"`
}

// IncidentResponse is the read-API projection of an incident. The raw webhook
// body is deliberately absent: an unauthenticated read API must not echo stored
// payloads (they sit in the incidents.raw column for debugging instead).
type IncidentResponse struct {
	ID                   uuid.UUID  `json:"id"`
	Source               string     `json:"source"`
	ExternalID           string     `json:"external_id"`
	Summary              string     `json:"summary"`
	Description          string     `json:"description"`
	IssueType            string     `json:"issue_type"`
	Priority             string     `json:"priority"`
	Labels               []string   `json:"labels"`
	Reporter             string     `json:"reporter"`
	Status               string     `json:"status"`
	LastDeliveryID       *string    `json:"last_delivery_id,omitempty"`
	Category             *string    `json:"category,omitempty"`
	Severity             *string    `json:"severity,omitempty"`
	PriorityScore        *int       `json:"priority_score,omitempty"`
	Confidence           *float64   `json:"confidence,omitempty"`
	Rationale            *string    `json:"rationale,omitempty"`
	SuggestedRunbook     *string    `json:"suggested_runbook,omitempty"`
	ClassificationSource *string    `json:"classification_source,omitempty"`
	ClassifiedAt         *time.Time `json:"classified_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// IncidentListResponse wraps a page of incidents.
type IncidentListResponse struct {
	Incidents []IncidentResponse `json:"incidents"`
	Count     int                `json:"count"`
}

func toIncidentResponse(inc domain.Incident) IncidentResponse {
	return IncidentResponse{
		ID:                   inc.ID,
		Source:               inc.Source,
		ExternalID:           inc.ExternalID,
		Summary:              inc.Summary,
		Description:          inc.Description,
		IssueType:            inc.IssueType,
		Priority:             inc.Priority,
		Labels:               inc.Labels,
		Reporter:             inc.Reporter,
		Status:               string(inc.Status),
		LastDeliveryID:       inc.LastDeliveryID,
		Category:             inc.Category,
		Severity:             inc.Severity,
		PriorityScore:        inc.PriorityScore,
		Confidence:           inc.Confidence,
		Rationale:            inc.Rationale,
		SuggestedRunbook:     inc.SuggestedRunbook,
		ClassificationSource: inc.ClassificationSource,
		ClassifiedAt:         inc.ClassifiedAt,
		CreatedAt:            inc.CreatedAt,
		UpdatedAt:            inc.UpdatedAt,
	}
}
