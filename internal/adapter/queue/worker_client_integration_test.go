//go:build integration

package queue_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kbadapter "github.com/ntttrang/ai-incident-triage/internal/adapter/kb"
	llmadapter "github.com/ntttrang/ai-incident-triage/internal/adapter/llm"
	postgresadapter "github.com/ntttrang/ai-incident-triage/internal/adapter/postgres"
	"github.com/ntttrang/ai-incident-triage/internal/adapter/queue"
	"github.com/ntttrang/ai-incident-triage/internal/domain"
	"github.com/ntttrang/ai-incident-triage/internal/service"
)

// TestWorkerClientProcessesJobEndToEnd is the only test that starts the
// consuming River client: enqueue → fetch → ClassifyWorker.Work → verdict
// persisted. The unit suites cover each piece in isolation; this one covers
// the client wiring (queue registration, job timeout, workers bundle) that
// would otherwise run for the first time in production.
func TestWorkerClientProcessesJobEndToEnd(t *testing.T) {
	ctx := context.Background()
	_, err := pool.Exec(ctx, "TRUNCATE river_job, incidents")
	require.NoError(t, err)

	id := uuid.New()
	_, err = pool.Exec(ctx,
		`INSERT INTO incidents (id, source, external_id, summary, raw, status)
		 VALUES ($1, 'jira', 'E2E-1', 'Prod db down', '{}'::jsonb, 'queued')`, id)
	require.NoError(t, err)

	insertClient, err := queue.NewInsertClient(pool)
	require.NoError(t, err)
	_, err = insertClient.Insert(ctx, queue.ClassifyIncidentArgs{IncidentID: id}, &river.InsertOpts{
		Queue:       queue.QueueName,
		MaxAttempts: queue.MaxAttempts,
	})
	require.NoError(t, err)

	// Real service graph minus the network: the heuristic classifier is
	// deterministic and needs no API key.
	repo := postgresadapter.NewIncidentRepository(pool)
	classifySvc := service.NewClassifyService(
		llmadapter.NewHeuristicClassifier(),
		kbadapter.NewStaticKB(),
		repo,
		slog.New(slog.DiscardHandler),
		nil,
	)

	log := slog.New(slog.DiscardHandler)
	client, err := queue.NewWorkerClient(pool, queue.NewWorkers(classifySvc, log))
	require.NoError(t, err)
	require.NoError(t, client.Start(ctx))
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		assert.NoError(t, client.Stop(stopCtx))
	}()

	// The verdict write and the job-row state flip are two writes; poll both
	// rather than asserting an ordering River does not guarantee.
	deadline := time.Now().Add(30 * time.Second)
	for {
		inc, err := repo.GetByID(ctx, id)
		require.NoError(t, err)

		var state string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT state FROM river_job WHERE kind = 'classify_incident' AND args->>'incident_id' = $1`,
			id).Scan(&state))

		if inc.Status == domain.StatusClassified && state == "completed" {
			require.NotNil(t, inc.ClassificationSource)
			assert.Equal(t, domain.ClassifySourceHeuristic, *inc.ClassificationSource)
			require.NotNil(t, inc.Category)
			assert.Equal(t, "database", *inc.Category)
			require.NotNil(t, inc.SuggestedRunbook, "static KB serves a runbook for every category")
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker client did not finish the job within 30s (incident status %q, job state %q)", inc.Status, state)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
