//go:build integration

package queue_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivertest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/adapter/queue"
)

var pool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		fmt.Println("TEST_DATABASE_URL not set; skipping integration tests")
		return
	}

	var err error
	pool, err = pgxpool.New(context.Background(), dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	migrator, err := migrate.New("file://../../../migrations", dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate init: %v\n", err)
		os.Exit(1)
	}
	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		fmt.Fprintf(os.Stderr, "migrate up: %v\n", err)
		os.Exit(1)
	}
	_, _ = migrator.Close()

	os.Exit(m.Run())
}

func truncate(t *testing.T) {
	t.Helper()
	_, err := pool.Exec(context.Background(), "TRUNCATE river_job")
	require.NoError(t, err)
}

// Compile-time proof that the River enqueuer satisfies the interface the
// postgres adapter declares — the two adapters stay decoupled at compile time
// and meet only in composition roots.
var _ interface {
	InsertClassifyTx(ctx context.Context, tx pgx.Tx, incidentID uuid.UUID) error
} = (*queue.Enqueuer)(nil)

func TestInsertClassifyTxInsertsOnClassifyQueue(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	client, err := queue.NewInsertClient(pool)
	require.NoError(t, err)
	enq := queue.NewEnqueuer(client)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)

	id := uuid.New()
	require.NoError(t, enq.InsertClassifyTx(ctx, tx, id))

	job := rivertest.RequireInsertedTx[*riverpgxv5.Driver, pgx.Tx, queue.ClassifyIncidentArgs](
		ctx, t, tx, queue.ClassifyIncidentArgs{IncidentID: id},
		&rivertest.RequireInsertedOpts{Queue: queue.QueueName, MaxAttempts: queue.MaxAttempts},
	)
	assert.Equal(t, "classify_incident", job.Kind)
}

func TestInsertClassifyTxRollsBackWithTransaction(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	client, err := queue.NewInsertClient(pool)
	require.NoError(t, err)
	enq := queue.NewEnqueuer(client)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, enq.InsertClassifyTx(ctx, tx, uuid.New()))
	require.NoError(t, tx.Rollback(ctx))

	var n int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM river_job WHERE kind = 'classify_incident'").Scan(&n))
	assert.Zero(t, n, "rolling back the caller transaction must discard the job")
}
