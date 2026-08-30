package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// ClassifyEnqueuer inserts classification jobs inside an existing transaction.
// The River enqueuer in internal/adapter/queue satisfies it structurally,
// keeping the two adapters decoupled.
type ClassifyEnqueuer interface {
	InsertClassifyTx(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID) error
}

// IngestStore makes webhook ingest atomic: the incident upsert and the
// classification-job enqueue share one transaction, so an incident can never
// exist without its job or vice versa.
type IngestStore struct {
	pool  *pgxpool.Pool
	queue ClassifyEnqueuer
}

// NewIngestStore returns an ingest store backed by the pool and queue.
func NewIngestStore(pool *pgxpool.Pool, queue ClassifyEnqueuer) *IngestStore {
	return &IngestStore{pool: pool, queue: queue}
}

// Receive implements domain.IncidentIngest. Status semantics:
//
//   - new issue                    -> INSERT with status 'queued' + one job
//   - existing issue, new delivery -> DO UPDATE back to 'queued', classification
//     fields reset, + one re-classify job
//   - existing issue, same delivery -> no write, no job (duplicate delivery)
//
// The status is written by this SQL rather than taken from the incident so the
// row always reflects the enqueue that commits alongside it.
func (s *IngestStore) Receive(ctx context.Context, inc *domain.Incident) (domain.UpsertOutcome, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin ingest tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after commit

	outcome, err := upsertIncidentTx(ctx, tx, inc)
	if err != nil {
		return "", err
	}

	if outcome != domain.UpsertDuplicate {
		if err := s.queue.InsertClassifyTx(ctx, tx, inc.ID); err != nil {
			return "", err
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit ingest tx: %w", err)
	}
	return outcome, nil
}

// upsertIncidentTx implements two-level idempotency in one statement:
//
//   - new issue                      -> INSERT, returns UpsertCreated
//   - existing issue, new delivery   -> DO UPDATE, returns UpsertUpdated
//   - existing issue, same delivery  -> WHERE excludes the row, no row
//     returned, returns UpsertDuplicate with zero writes
//
// "xmax = 0" is the Postgres idiom distinguishing a fresh INSERT (xmax = 0)
// from a row updated by the current transaction (xmax = txid).
func upsertIncidentTx(ctx context.Context, tx pgx.Tx, inc *domain.Incident) (domain.UpsertOutcome, error) {
	const q = `
		INSERT INTO incidents (
			source, external_id, summary, description, issue_type, priority,
			labels, reporter, status, raw, last_delivery_id
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'queued', $9::jsonb, $10)
		ON CONFLICT (source, external_id) DO UPDATE SET
			summary              = EXCLUDED.summary,
			description          = EXCLUDED.description,
			issue_type           = EXCLUDED.issue_type,
			priority             = EXCLUDED.priority,
			labels               = EXCLUDED.labels,
			reporter            = EXCLUDED.reporter,
			raw                  = EXCLUDED.raw,
			last_delivery_id     = EXCLUDED.last_delivery_id,
			-- a new delivery re-runs classification: reset the previous verdict
			-- and put the row back in the queue so the job and row agree.
			status               = 'queued',
			category             = NULL,
			severity             = NULL,
			priority_score       = NULL,
			confidence           = NULL,
			rationale            = NULL,
			suggested_runbook    = NULL,
			classification_source = NULL,
			classified_at        = NULL,
			updated_at           = now()
		WHERE incidents.last_delivery_id IS DISTINCT FROM EXCLUDED.last_delivery_id
		RETURNING id, (xmax = 0) AS created`

	var id uuid.UUID
	var created bool
	err := tx.QueryRow(ctx, q,
		inc.Source, inc.ExternalID, inc.Summary, inc.Description, inc.IssueType,
		inc.Priority, inc.Labels, inc.Reporter, string(inc.Raw),
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
