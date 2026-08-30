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
	// river_job too: enqueue assertions must not see jobs from earlier tests.
	_, err := pool.Exec(context.Background(), "TRUNCATE incidents, river_job")
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

func countJobs(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		"SELECT count(*) FROM river_job WHERE kind = 'classify_incident'").Scan(&n))
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

func TestGetByIDNotFound(t *testing.T) {
	truncate(t)
	repo := postgres.NewIncidentRepository(pool)

	_, err := repo.GetByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, domain.ErrNotFound)
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
		{"OPS-mid", "queued", nil, "2026-08-26T10:00:00Z"},
		{"OPS-new", "queued", strptr("high"), "2026-08-27T10:00:00Z"},
	} {
		_, err := pool.Exec(ctx, seed, row.key, row.status, row.severity, row.created)
		require.NoError(t, err)
	}

	repo := postgres.NewIncidentRepository(pool)

	got, err := repo.List(ctx, domain.IncidentFilter{Status: "queued"})
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

func TestSaveClassificationStoresVerdict(t *testing.T) {
	truncate(t)
	ctx := context.Background()
	repo := postgres.NewIncidentRepository(pool)

	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO incidents (source, external_id, summary, raw, status)
		 VALUES ('jira', 'OPS-1', 'db down', '{}'::jsonb, 'queued') RETURNING id`,
	).Scan(&id))

	cls := &domain.Classification{
		Category:      "database",
		Severity:      "critical",
		PriorityScore: 85,
		Confidence:    0.9,
		Rationale:     "summary says database down",
		Source:        domain.ClassifySourceHeuristic,
	}
	require.NoError(t, repo.SaveClassification(ctx, id, cls))

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, domain.StatusClassified, got.Status)
	require.NotNil(t, got.Category)
	assert.Equal(t, "database", *got.Category)
	require.NotNil(t, got.Severity)
	assert.Equal(t, "critical", *got.Severity)
	require.NotNil(t, got.PriorityScore)
	assert.Equal(t, 85, *got.PriorityScore)
	require.NotNil(t, got.ClassificationSource)
	assert.Equal(t, domain.ClassifySourceHeuristic, *got.ClassificationSource)
	require.NotNil(t, got.ClassifiedAt, "classified_at must be stamped by the write")
}

func TestSaveClassificationRejectsUnknownVocabulary(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO incidents (source, external_id, summary, raw, status)
		 VALUES ('jira', 'OPS-1', 's', '{}'::jsonb, 'queued') RETURNING id`,
	).Scan(&id))

	repo := postgres.NewIncidentRepository(pool)

	err := repo.SaveClassification(ctx, id, &domain.Classification{Category: " vibes", Severity: "critical"})
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	err = repo.SaveClassification(ctx, id, &domain.Classification{Category: "database", Severity: "ultra"})
	require.ErrorIs(t, err, domain.ErrInvalidInput)

	// The rejected writes must have left the row untouched.
	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusQueued, got.Status)
	assert.Nil(t, got.Category)
}

func TestSaveClassificationNotFound(t *testing.T) {
	truncate(t)
	repo := postgres.NewIncidentRepository(pool)

	cls := &domain.Classification{Category: "database", Severity: "low"}
	err := repo.SaveClassification(context.Background(), uuid.New(), cls)
	require.ErrorIs(t, err, domain.ErrNotFound)
}

func TestMarkFailedFlipsStatus(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	var id uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO incidents (source, external_id, summary, raw, status)
		 VALUES ('jira', 'OPS-1', 's', '{}'::jsonb, 'queued') RETURNING id`,
	).Scan(&id))

	repo := postgres.NewIncidentRepository(pool)
	require.NoError(t, repo.MarkFailed(ctx, id))

	got, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusFailed, got.Status)

	err = repo.MarkFailed(ctx, uuid.New())
	require.ErrorIs(t, err, domain.ErrNotFound)
}
