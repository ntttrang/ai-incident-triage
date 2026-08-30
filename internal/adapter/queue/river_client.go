// Package queue wires River (Postgres-native job queue) clients.
//
// One constructor serves two compositions: the API process builds an
// insert-only client (no Queues, never started — it only enqueues inside
// ingest transactions), while the worker process builds a consuming client
// with the classify queue configured and starts it.
package queue

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// QueueName is the only queue this service uses. Jobs land on it only if the
// worker's client also configures it — an unconfigured queue accepts inserts
// silently and the jobs sit available forever.
const QueueName = "classify"

// MaxAttempts bounds classification retries before a job is discarded and the
// incident marked failed.
const MaxAttempts = 4

// JobTimeout is the Work-context budget. It deliberately exceeds the LLM
// context (see llm package) with margin: when a slow LLM burns its own
// deadline, this context must still be alive for the heuristic fallback and
// the result write, or degradation never fires for the failure class it
// exists for.
const JobTimeout = 150 * time.Second

// QueueMaxWorkers bounds concurrent classification jobs per worker process.
const QueueMaxWorkers = 3

// WriteMargin is the slice of JobTimeout reserved for the heuristic fallback
// and the verdict write after the LLM deadline fires. CheckLLMTimeoutMargin
// enforces that an LLM budget leaves at least this much room.
const WriteMargin = 30 * time.Second

// CheckLLMTimeoutMargin rejects an LLM request budget that would outlive the
// Work context. The worker process calls it at startup: with a too-large
// OPENAI_TIMEOUT_SECS the parent context cancels the LLM call mid-flight,
// degradation never fires, and every attempt also fails its result write.
func CheckLLMTimeoutMargin(llmTimeout time.Duration) error {
	if llmTimeout <= 0 || llmTimeout+WriteMargin > JobTimeout {
		return fmt.Errorf("llm timeout %s must be positive and leave a %s write margin inside the %s job budget (lower OPENAI_TIMEOUT_SECS)",
			llmTimeout, WriteMargin, JobTimeout)
	}
	return nil
}

// NewInsertClient returns an enqueue-only River client for the API process.
// It never starts and consumes no jobs.
func NewInsertClient(pool *pgxpool.Pool) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, fmt.Errorf("river insert client: %w", err)
	}
	return client, nil
}

// NewWorkerClient returns a consuming client with the classify queue and the
// given workers registered. Discarded (dead-letter) jobs are retained
// forever so the replay story never silently expires.
func NewWorkerClient(pool *pgxpool.Pool, workers *river.Workers) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			QueueName: {MaxWorkers: QueueMaxWorkers},
		},
		Workers:                     workers,
		JobTimeout:                  JobTimeout,
		DiscardedJobRetentionPeriod: -1,
	})
	if err != nil {
		return nil, fmt.Errorf("river worker client: %w", err)
	}
	return client, nil
}

// Enqueuer inserts classification jobs. It satisfies the ClassifyEnqueuer
// interface declared in internal/adapter/postgres structurally.
type Enqueuer struct {
	client *river.Client[pgx.Tx]
}

// NewEnqueuer wraps a River client for transactional enqueueing.
func NewEnqueuer(client *river.Client[pgx.Tx]) *Enqueuer {
	return &Enqueuer{client: client}
}

// InsertClassifyTx enqueues one classification job inside an existing
// transaction, so it commits or rolls back with the incident upsert.
func (e *Enqueuer) InsertClassifyTx(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, ClassifyIncidentArgs{IncidentID: incidentID}, &river.InsertOpts{
		Queue:       QueueName,
		MaxAttempts: MaxAttempts,
	})
	if err != nil {
		return fmt.Errorf("enqueue classify job for %s: %w", incidentID, err)
	}
	return nil
}
