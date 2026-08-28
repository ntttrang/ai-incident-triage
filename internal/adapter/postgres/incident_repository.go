// Package postgres implements incident persistence on PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// incidentColumns is every column the read API may select. raw is deliberately
// absent: stored webhook bodies never leave the database through list/get.
const incidentColumns = `
	id, source, external_id, summary, description, issue_type, priority,
	labels, reporter, status, last_delivery_id,
	category, severity, priority_score, confidence, rationale, suggested_runbook,
	classified_at, created_at, updated_at`

// IncidentRepository stores incidents via a pgx connection pool.
type IncidentRepository struct {
	pool *pgxpool.Pool
}

// NewIncidentRepository returns a repository backed by the given pool.
func NewIncidentRepository(pool *pgxpool.Pool) *IncidentRepository {
	return &IncidentRepository{pool: pool}
}

// UpsertFromWebhook implements two-level idempotency in one statement:
//
//   - new issue                      -> INSERT, returns UpsertCreated
//   - existing issue, new delivery   -> DO UPDATE, returns UpsertUpdated
//   - existing issue, same delivery  -> WHERE excludes the row, no row
//     returned, returns UpsertDuplicate with zero writes
//
// "xmax = 0" is the Postgres idiom distinguishing a fresh INSERT (xmax = 0)
// from a row updated by the current transaction (xmax = txid).
func (r *IncidentRepository) UpsertFromWebhook(ctx context.Context, inc *domain.Incident) (domain.UpsertOutcome, error) {
	const q = `
		INSERT INTO incidents (
			source, external_id, summary, description, issue_type, priority,
			labels, reporter, status, raw, last_delivery_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11)
		ON CONFLICT (source, external_id) DO UPDATE SET
			summary          = EXCLUDED.summary,
			description      = EXCLUDED.description,
			issue_type       = EXCLUDED.issue_type,
			priority         = EXCLUDED.priority,
			labels           = EXCLUDED.labels,
			reporter         = EXCLUDED.reporter,
			raw              = EXCLUDED.raw,
			last_delivery_id = EXCLUDED.last_delivery_id,
			updated_at       = now()
		WHERE incidents.last_delivery_id IS DISTINCT FROM EXCLUDED.last_delivery_id
		RETURNING id, (xmax = 0) AS created`

	var id uuid.UUID
	var created bool
	err := r.pool.QueryRow(ctx, q,
		inc.Source, inc.ExternalID, inc.Summary, inc.Description, inc.IssueType,
		inc.Priority, inc.Labels, inc.Reporter, string(inc.Status), string(inc.Raw),
		inc.LastDeliveryID,
	).Scan(&id, &created)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.UpsertDuplicate, nil
		}
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// The only unhandled unique index is (source, last_delivery_id):
			// a delivery identifier reused by a different issue.
			return "", fmt.Errorf("upsert incident: %w", domain.ErrConflict)
		}
		return "", fmt.Errorf("upsert incident: %w", err)
	}

	inc.ID = id
	if created {
		return domain.UpsertCreated, nil
	}
	return domain.UpsertUpdated, nil
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
		&inc.Confidence, &inc.Rationale, &inc.SuggestedRunbook, &inc.ClassifiedAt,
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
