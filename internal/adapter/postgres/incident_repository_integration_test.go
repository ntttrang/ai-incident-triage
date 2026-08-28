//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/adapter/postgres"
	"github.com/ntttrang/ai-incident-triage/internal/domain"
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
	_, err := pool.Exec(context.Background(), "TRUNCATE incidents")
	require.NoError(t, err)
}

// incident builds an inbound webhook projection; delivery == "" means the
// delivery header was absent.
func incident(key, delivery, summary string) *domain.Incident {
	inc := &domain.Incident{
		Source:     "jira",
		ExternalID: key,
		Summary:    summary,
		Status:     domain.StatusReceived,
		Labels:     []string{},
		Raw:        []byte(fmt.Sprintf(`{"issue":{"key":%q,"summary":%q}}`, key, summary)),
	}
	if delivery != "" {
		inc.LastDeliveryID = &delivery
	}
	return inc
}

func countIncidents(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM incidents").Scan(&n))
	return n
}

func rowState(t *testing.T, key string) (updatedAt time.Time, delivery *string, summary string, raw string) {
	t.Helper()
	require.NoError(t, pool.QueryRow(context.Background(),
		"SELECT updated_at, last_delivery_id, summary, raw::text FROM incidents WHERE source='jira' AND external_id=$1", key,
	).Scan(&updatedAt, &delivery, &summary, &raw))
	return updatedAt, delivery, summary, raw
}

func strptr(s string) *string { return &s }

func TestUpsertCreated(t *testing.T) {
	truncate(t)
	repo := postgres.NewIncidentRepository(pool)
	ctx := context.Background()

	inc := incident("OPS-1", "d-1", "db down")
	outcome, err := repo.UpsertFromWebhook(ctx, inc)
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertCreated, outcome)
	require.NotEqual(t, uuid.Nil, inc.ID, "insert must return the row id")
	assert.Equal(t, 1, countIncidents(t))

	got, err := repo.GetByID(ctx, inc.ID)
	require.NoError(t, err)
	assert.Equal(t, "db down", got.Summary)
	assert.Equal(t, domain.StatusReceived, got.Status)
	assert.NotNil(t, got.LastDeliveryID)
}

func TestUpsertDuplicateWritesNothing(t *testing.T) {
	truncate(t)
	repo := postgres.NewIncidentRepository(pool)
	ctx := context.Background()

	_, err := repo.UpsertFromWebhook(ctx, incident("OPS-1", "d-1", "db down"))
	require.NoError(t, err)
	beforeUpdate, beforeDelivery, beforeSummary, beforeRaw := rowState(t, "OPS-1")

	time.Sleep(10 * time.Millisecond) // updated_at would move if a write happened

	outcome, err := repo.UpsertFromWebhook(ctx, incident("OPS-1", "d-1", "db down CHANGED"))
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertDuplicate, outcome)

	afterUpdate, afterDelivery, afterSummary, afterRaw := rowState(t, "OPS-1")
	assert.True(t, beforeUpdate.Equal(afterUpdate), "duplicate must not touch updated_at")
	assert.Equal(t, beforeDelivery, afterDelivery)
	assert.Equal(t, beforeSummary, afterSummary)
	assert.Equal(t, beforeRaw, afterRaw)
	assert.Equal(t, 1, countIncidents(t))
}

func TestUpsertUpdatedRefreshesRow(t *testing.T) {
	truncate(t)
	repo := postgres.NewIncidentRepository(pool)
	ctx := context.Background()

	_, err := repo.UpsertFromWebhook(ctx, incident("OPS-1", "d-1", "db down"))
	require.NoError(t, err)

	outcome, err := repo.UpsertFromWebhook(ctx, incident("OPS-1", "d-2", "db recovering"))
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertUpdated, outcome)

	_, delivery, summary, raw := rowState(t, "OPS-1")
	require.NotNil(t, delivery)
	assert.Equal(t, "d-2", *delivery)
	assert.Equal(t, "db recovering", summary)
	// JSONB re-serializes with canonical spacing; compare semantically.
	assert.JSONEq(t, `{"issue":{"key":"OPS-1","summary":"db recovering"}}`, raw, "update must refresh the stored raw body")
	assert.Equal(t, 1, countIncidents(t), "update must not create a second row")
}

func TestUpsertWithoutDeliveryHeader(t *testing.T) {
	truncate(t)
	repo := postgres.NewIncidentRepository(pool)
	ctx := context.Background()

	outcome, err := repo.UpsertFromWebhook(ctx, incident("OPS-1", "", "db down"))
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertCreated, outcome)

	// NULLs are ignored by the delivery unique constraint, so a headerless
	// delivery inserts; a second headerless delivery of the same body cannot be
	// distinguished from a replay and is treated as a duplicate.
	outcome, err = repo.UpsertFromWebhook(ctx, incident("OPS-1", "", "db down"))
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertDuplicate, outcome)

	// A later delivery WITH a header still updates the row.
	outcome, err = repo.UpsertFromWebhook(ctx, incident("OPS-1", "d-9", "db down"))
	require.NoError(t, err)
	assert.Equal(t, domain.UpsertUpdated, outcome)
}

func TestUpsertDeliveryIDCollisionConflicts(t *testing.T) {
	truncate(t)
	repo := postgres.NewIncidentRepository(pool)
	ctx := context.Background()

	_, err := repo.UpsertFromWebhook(ctx, incident("OPS-1", "shared-delivery", "a"))
	require.NoError(t, err)

	_, err = repo.UpsertFromWebhook(ctx, incident("OPS-2", "shared-delivery", "b"))
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrConflict)
}

func TestListFiltersAndOrder(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	seed := `INSERT INTO incidents (source, external_id, summary, raw, status, severity, created_at)
	         VALUES ('jira', $1, 's', '{}'::jsonb, $2, $3, $4)`
	for _, row := range []struct {
		key      string
		status   string
		severity *string
		created  string
	}{
		{"OPS-old", "classified", strptr("critical"), "2026-08-25T10:00:00Z"},
		{"OPS-mid", "received", nil, "2026-08-26T10:00:00Z"},
		{"OPS-new", "received", strptr("high"), "2026-08-27T10:00:00Z"},
	} {
		_, err := pool.Exec(ctx, seed, row.key, row.status, row.severity, row.created)
		require.NoError(t, err)
	}

	repo := postgres.NewIncidentRepository(pool)

	got, err := repo.List(ctx, domain.IncidentFilter{Status: "received"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "OPS-new", got[0].ExternalID, "newest first")

	got, err = repo.List(ctx, domain.IncidentFilter{Severity: "critical"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Severity)
	assert.Equal(t, "critical", *got[0].Severity)

	got, err = repo.List(ctx, domain.IncidentFilter{Limit: 1})
	require.NoError(t, err)
	assert.Len(t, got, 1)

	got, err = repo.List(ctx, domain.IncidentFilter{Offset: 1})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "OPS-mid", got[0].ExternalID, "offset skips the newest row")

	got, err = repo.List(ctx, domain.IncidentFilter{})
	require.NoError(t, err)
	assert.Len(t, got, 3)
}

func TestGetByIDNotFound(t *testing.T) {
	truncate(t)
	repo := postgres.NewIncidentRepository(pool)

	_, err := repo.GetByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, domain.ErrNotFound)
}
