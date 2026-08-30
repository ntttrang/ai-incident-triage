//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/adapter/postgres"
	"github.com/ntttrang/ai-incident-triage/internal/adapter/queue"
	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// newIngest wires the real ingest path: IngestStore + a River insert-only
// client, exactly as cmd/api composes them. This also proves queue.Enqueuer
// satisfies postgres.ClassifyEnqueuer structurally at compile time.
func newIngest(t *testing.T) (*postgres.IngestStore, *riverpgxv5.Driver) {
	t.Helper()
	client, err := queue.NewInsertClient(pool)
	require.NoError(t, err)
	store := postgres.NewIngestStore(pool, queue.NewEnqueuer(client))
	return store, riverpgxv5.New(pool)
}

// failingEnqueuer simulates the queue being unavailable mid-transaction.
type failingEnqueuer struct{ err error }

func (f failingEnqueuer) InsertClassifyTx(context.Context, pgx.Tx, uuid.UUID) error {
	return f.err
}

func TestReceiveCreatedQueuesIncidentAndJob(t *testing.T) {
	truncate(t)
	store, driver := newIngest(t)
	ctx := context.Background()

	inc := incident("OPS-1", "d-1", "db down")
	outcome, err := store.Receive(ctx, inc)
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertCreated, outcome)
	require.NotEqual(t, uuid.Nil, inc.ID, "insert must return the row id")
	assert.Equal(t, 1, countIncidents(t))

	// The row is born queued: ingest and enqueue commit together.
	repo := postgres.NewIncidentRepository(pool)
	got, err := repo.GetByID(ctx, inc.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusQueued, got.Status)
	assert.NotNil(t, got.LastDeliveryID)

	// Exactly one classify job carrying this incident id, on the classify
	// queue, with the configured retry budget.
	job := rivertest.RequireInserted[*riverpgxv5.Driver, pgx.Tx, queue.ClassifyIncidentArgs](
		ctx, t, driver, queue.ClassifyIncidentArgs{IncidentID: inc.ID},
		&rivertest.RequireInsertedOpts{Queue: queue.QueueName, MaxAttempts: queue.MaxAttempts},
	)
	assert.Equal(t, inc.ID, job.Args.IncidentID)
	assert.Equal(t, 1, countJobs(t))
}

func TestReceiveDuplicateWritesNothingAndEnqueuesNothing(t *testing.T) {
	truncate(t)
	store, _ := newIngest(t)
	ctx := context.Background()

	_, err := store.Receive(ctx, incident("OPS-1", "d-1", "db down"))
	require.NoError(t, err)
	beforeUpdate, beforeDelivery, beforeSummary, beforeRaw := rowState(t, "OPS-1")

	time.Sleep(10 * time.Millisecond) // updated_at would move if a write happened

	outcome, err := store.Receive(ctx, incident("OPS-1", "d-1", "db down CHANGED"))
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertDuplicate, outcome)

	afterUpdate, afterDelivery, afterSummary, afterRaw := rowState(t, "OPS-1")
	assert.True(t, beforeUpdate.Equal(afterUpdate), "duplicate must not touch updated_at")
	assert.Equal(t, beforeDelivery, afterDelivery)
	assert.Equal(t, beforeSummary, afterSummary)
	assert.Equal(t, beforeRaw, afterRaw)
	assert.Equal(t, 1, countIncidents(t))
	assert.Equal(t, 1, countJobs(t), "duplicate delivery must not enqueue a second job")
}

func TestReceiveUpdatedRequeuesAndResetsVerdict(t *testing.T) {
	truncate(t)
	store, _ := newIngest(t)
	ctx := context.Background()

	inc := incident("OPS-1", "d-1", "db down")
	_, err := store.Receive(ctx, inc)
	require.NoError(t, err)

	// Simulate a completed classification cycle...
	_, err = pool.Exec(ctx,
		`UPDATE incidents SET status='classified', category='database', severity='high',
		 priority_score=60, confidence=0.8, rationale='r', suggested_runbook='u',
		 classification_source='llm', classified_at=now() WHERE id=$1`, inc.ID)
	require.NoError(t, err)

	// ...then a new delivery arrives.
	outcome, err := store.Receive(ctx, incident("OPS-1", "d-2", "db recovering"))
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertUpdated, outcome)

	repo := postgres.NewIncidentRepository(pool)
	got, err := repo.GetByID(ctx, inc.ID)
	require.NoError(t, err)
	require.Equal(t, domain.StatusQueued, got.Status)
	require.NotNil(t, got.LastDeliveryID)
	assert.Equal(t, "d-2", *got.LastDeliveryID)
	assert.Equal(t, "db recovering", got.Summary)
	assert.Nil(t, got.Category, "re-classification must reset the previous verdict")
	assert.Nil(t, got.Severity)
	assert.Nil(t, got.ClassificationSource)
	assert.Nil(t, got.ClassifiedAt)
	assert.Equal(t, 2, countJobs(t), "new delivery must enqueue a re-classify job")
	assert.Equal(t, 1, countIncidents(t), "update must not create a second row")
}

func TestReceiveWithoutDeliveryHeader(t *testing.T) {
	truncate(t)
	store, _ := newIngest(t)
	ctx := context.Background()

	outcome, err := store.Receive(ctx, incident("OPS-1", "", "db down"))
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertCreated, outcome)

	// NULLs are ignored by the delivery unique constraint, so a headerless
	// delivery inserts; a second headerless delivery of the same body cannot be
	// distinguished from a replay and is treated as a duplicate.
	outcome, err = store.Receive(ctx, incident("OPS-1", "", "db down"))
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertDuplicate, outcome)

	// A later delivery WITH a header still updates the row.
	outcome, err = store.Receive(ctx, incident("OPS-1", "d-9", "db down"))
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertUpdated, outcome)
	assert.Equal(t, 2, countJobs(t))
}

func TestReceiveDeliveryIDCollisionConflicts(t *testing.T) {
	truncate(t)
	store, _ := newIngest(t)
	ctx := context.Background()

	_, err := store.Receive(ctx, incident("OPS-1", "shared-delivery", "a"))
	require.NoError(t, err)

	_, err = store.Receive(ctx, incident("OPS-2", "shared-delivery", "b"))
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrConflict)
	assert.Equal(t, 1, countIncidents(t), "conflicting insert must roll back")
}

func TestReceiveAtomicWhenEnqueueFails(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	boom := errors.New("queue unavailable")
	store := postgres.NewIngestStore(pool, failingEnqueuer{err: boom})

	_, err := store.Receive(ctx, incident("OPS-1", "d-1", "db down"))
	require.ErrorIs(t, err, boom)

	assert.Equal(t, 0, countIncidents(t), "a failed enqueue must roll back the incident row")
	assert.Equal(t, 0, countJobs(t))
}
