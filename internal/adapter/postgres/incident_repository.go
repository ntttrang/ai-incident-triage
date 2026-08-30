// Package postgres implements incident persistence on PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// incidentColumns is every column the read API may select. raw is deliberately
// absent: stored webhook bodies never leave the database through list/get.
const incidentColumns = `
	id, source, external_id, summary, description, issue_type, priority,
	labels, reporter, status, last_delivery_id,
	category, severity, priority_score, confidence, rationale, suggested_runbook,
	classification_source, classified_at, created_at, updated_at`

// IncidentRepository stores incidents via a pgx connection pool.
type IncidentRepository struct {
	pool *pgxpool.Pool
}

// NewIncidentRepository returns a repository backed by the given pool.
func NewIncidentRepository(pool *pgxpool.Pool) *IncidentRepository {
	return &IncidentRepository{pool: pool}
}

// SaveClassification stores the verdict and flips the row to classified.
// Unknown categories/severities are rejected so a hallucinating model cannot
// poison filter indexes with one-off values.
func (r *IncidentRepository) SaveClassification(ctx context.Context, id uuid.UUID, cls *domain.Classification) error {
	if !domain.KnownCategory(cls.Category) || !domain.KnownSeverity(cls.Severity) {
		return fmt.Errorf("save classification %s: %w (category=%q severity=%q)", id, domain.ErrInvalidInput, cls.Category, cls.Severity)
	}
	const q = `
		UPDATE incidents SET
			category = $2,
			severity = $3,
			priority_score = $4,
			confidence = $5,
			rationale = $6,
			suggested_runbook = $7,
			classification_source = $8,
			classified_at = now(),
			status = 'classified',
			updated_at = now()
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q,
		id, cls.Category, cls.Severity, cls.PriorityScore, cls.Confidence,
		cls.Rationale, cls.SuggestedRunbook, cls.Source,
	)
	if err != nil {
		return fmt.Errorf("save classification: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("save classification %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// MarkFailed records that classification exhausted its retries.
func (r *IncidentRepository) MarkFailed(ctx context.Context, id uuid.UUID) error {
	const q = `
		UPDATE incidents SET status = 'failed', updated_at = now()
		WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("mark failed %s: %w", id, domain.ErrNotFound)
	}
	return nil
}

// List returns incidents matching the filter, newest first, without raw bodies.
func (r *IncidentRepository) List(ctx context.Context, filter domain.IncidentFilter) ([]domain.Incident, error) {
	where := make([]string, 0, 2)
	args := make([]any, 0, 4)
	if filter.Status != "" {
		args = append(args, filter.Status)
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if filter.Severity != "" {
		args = append(args, filter.Severity)
		where = append(where, fmt.Sprintf("severity = $%d", len(args)))
	}

	limit, offset := filter.Limit, filter.Offset
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)

	q := "SELECT " + incidentColumns + " FROM incidents"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()

	incidents := make([]domain.Incident, 0)
	for rows.Next() {
		inc, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		incidents = append(incidents, *inc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	return incidents, nil
}

// GetByID returns one incident without its raw body.
func (r *IncidentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Incident, error) {
	q := "SELECT " + incidentColumns + " FROM incidents WHERE id = $1"
	inc, err := scanIncident(r.pool.QueryRow(ctx, q, id))
	if err != nil {
		return nil, fmt.Errorf("find incident: %w", err)
	}
	return inc, nil
}

func scanIncident(row pgx.Row) (*domain.Incident, error) {
	var inc domain.Incident
	var status string
	err := row.Scan(
		&inc.ID, &inc.Source, &inc.ExternalID, &inc.Summary, &inc.Description,
		&inc.IssueType, &inc.Priority, &inc.Labels, &inc.Reporter, &status,
		&inc.LastDeliveryID, &inc.Category, &inc.Severity, &inc.PriorityScore,
		&inc.Confidence, &inc.Rationale, &inc.SuggestedRunbook,
		&inc.ClassificationSource, &inc.ClassifiedAt,
		&inc.CreatedAt, &inc.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan incident: %w", err)
	}
	if inc.Labels == nil {
		inc.Labels = []string{}
	}
	inc.Status = domain.IncidentStatus(status)
	return &inc, nil
}
