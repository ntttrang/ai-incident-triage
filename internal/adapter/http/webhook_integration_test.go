//go:build integration

package httpadapter_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	httpadapter "github.com/ntttrang/ai-incident-triage/internal/adapter/http"
	postgresadapter "github.com/ntttrang/ai-incident-triage/internal/adapter/postgres"
	"github.com/ntttrang/ai-incident-triage/internal/platform/logger"
	"github.com/ntttrang/ai-incident-triage/internal/platform/metrics"
	"github.com/ntttrang/ai-incident-triage/internal/service"
)

const testSecret = "integration-test-secret"

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

	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	truncate(t)

	repo := postgresadapter.NewIncidentRepository(pool)
	svc := service.NewIncidentService(repo, logger.New("error"))
	log := logger.New("error")

	r := httpadapter.NewRouter(httpadapter.Dependencies{
		Log:       log,
		Metrics:   metrics.New(),
		Health:    httpadapter.NewHealthHandler(pool),
		Webhook:   httpadapter.NewWebhookHandler(svc, testSecret, log),
		Incidents: httpadapter.NewIncidentHandler(svc, log),
		Env:       "test",
	})
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	return server
}

func truncate(t *testing.T) {
	t.Helper()
	_, err := pool.Exec(context.Background(), "TRUNCATE incidents")
	require.NoError(t, err)
}

func signedRequest(t *testing.T, method, url string, body []byte, deliveryID string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write(body)
	req.Header.Set("X-Hub-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	if deliveryID != "" {
		req.Header.Set("X-Atlassian-Webhook-Identifier", deliveryID)
	}
	return req
}

func issueBody(summary string) []byte {
	return []byte(fmt.Sprintf(`{"timestamp":1785000000000,"webhookEvent":"jira:issue_created","issue":{"id":40001,"key":"OPS-42","fields":{"summary":%q,"description":{"type":"doc","version":1,"content":[{"type":"paragraph","content":[{"type":"text","text":"details"}]}]},"issuetype":{"name":"Bug"},"priority":{"name":"High"},"labels":["prod"],"reporter":{"displayName":"Grace Hopper"}}}}`, summary))
}

func do(t *testing.T, client *http.Client, req *http.Request) (int, map[string]any) {
	t.Helper()
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var payload map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &payload)
	}
	return resp.StatusCode, payload
}

func TestWebhookIngestLifecycle(t *testing.T) {
	server := newServer(t)
	client := server.Client()
	url := server.URL + "/api/v1/webhooks/jira"

	// 1. New issue -> 202 created.
	code, payload := do(t, client, signedRequest(t, http.MethodPost, url, issueBody("Prod db down"), "delivery-1"))
	require.Equal(t, http.StatusAccepted, code, "payload: %v", payload)
	assert.Equal(t, "created", payload["outcome"])
	incidentID, ok := payload["id"].(string)
	require.True(t, ok, "created response carries the id")

	// 2. Same delivery replayed -> 200 duplicate, no write.
	code, payload = do(t, client, signedRequest(t, http.MethodPost, url, issueBody("Prod db down"), "delivery-1"))
	require.Equal(t, http.StatusOK, code, "payload: %v", payload)
	assert.Equal(t, "duplicate", payload["outcome"])

	// 3. New delivery, same issue -> 200 updated.
	code, payload = do(t, client, signedRequest(t, http.MethodPost, url, issueBody("Prod db recovering"), "delivery-2"))
	require.Equal(t, http.StatusOK, code, "payload: %v", payload)
	assert.Equal(t, "updated", payload["outcome"])

	// Still exactly one row.
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM incidents").Scan(&n))
	assert.Equal(t, 1, n)

	// 4. Read API never exposes the stored raw body.
	code, payload = do(t, client, signedRequest(t, http.MethodGet, server.URL+"/api/v1/incidents", nil, ""))
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, payload, "raw", "list response must omit raw")

	incidents, ok := payload["incidents"].([]any)
	require.True(t, ok)
	require.Len(t, incidents, 1)
	first := incidents[0].(map[string]any)
	assert.NotContains(t, first, "raw")
	assert.Equal(t, "Prod db recovering", first["summary"], "update refreshed the projection")
	assert.Equal(t, "received", first["status"])

	code, payload = do(t, client, signedRequest(t, http.MethodGet, server.URL+"/api/v1/incidents/"+incidentID, nil, ""))
	require.Equal(t, http.StatusOK, code)
	assert.NotContains(t, payload, "raw", "get response must omit raw")

	// 5. Unknown id -> 404; malformed id -> 400.
	code, _ = do(t, client, signedRequest(t, http.MethodGet, server.URL+"/api/v1/incidents/"+uuid.New().String(), nil, ""))
	assert.Equal(t, http.StatusNotFound, code)
	code, _ = do(t, client, signedRequest(t, http.MethodGet, server.URL+"/api/v1/incidents/not-a-uuid", nil, ""))
	assert.Equal(t, http.StatusBadRequest, code)
}

func TestWebhookSignatureEnforced(t *testing.T) {
	server := newServer(t)
	client := server.Client()
	url := server.URL + "/api/v1/webhooks/jira"
	body := issueBody("Prod db down")

	// Valid signature goes through.
	code, _ := do(t, client, signedRequest(t, http.MethodPost, url, body, "delivery-1"))
	require.Equal(t, http.StatusAccepted, code)

	// Tampered signature: sign different bytes.
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte("other body"))
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("X-Atlassian-Webhook-Identifier", "delivery-2")
	code, _ = do(t, client, req)
	assert.Equal(t, http.StatusUnauthorized, code)

	// Missing header.
	req2, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	code, _ = do(t, client, req2)
	assert.Equal(t, http.StatusUnauthorized, code)

	// Wrong scheme prefix.
	req3, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req3.Header.Set("X-Hub-Signature", "sha1=abcdef")
	code, _ = do(t, client, req3)
	assert.Equal(t, http.StatusUnauthorized, code)

	// Nothing extra was stored by rejected requests.
	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM incidents").Scan(&n))
	assert.Equal(t, 1, n)
}

func TestWebhookRejectsUnusableBodies(t *testing.T) {
	server := newServer(t)
	client := server.Client()
	url := server.URL + "/api/v1/webhooks/jira"

	cases := map[string][]byte{
		"invalid json":    []byte(`{"issue":`),
		"missing summary": []byte(`{"webhookEvent":"jira:issue_created","issue":{"key":"OPS-1","fields":{}}}`),
		"missing issue":   []byte(`{"webhookEvent":"comment_created","comment":{"body":"hi"}}`),
		"empty issue key": []byte(`{"webhookEvent":"jira:issue_created","issue":{"key":"","fields":{"summary":"s"}}}`),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			code, payload := do(t, client, signedRequest(t, http.MethodPost, url, body, "delivery-x"))
			assert.Equal(t, http.StatusBadRequest, code, "payload: %v", payload)
		})
	}

	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM incidents").Scan(&n))
	assert.Equal(t, 0, n, "rejected bodies must not store anything")
}

func TestWebhookBodyLimit(t *testing.T) {
	server := newServer(t)
	client := server.Client()

	// Valid signature, oversized body: rejected before buffering 1 MiB+.
	big := bytes.Repeat([]byte("a"), (1<<20)+16)
	code, _ := do(t, client, signedRequest(t, http.MethodPost, server.URL+"/api/v1/webhooks/jira", big, "delivery-big"))
	assert.Equal(t, http.StatusRequestEntityTooLarge, code)

	var n int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT count(*) FROM incidents").Scan(&n))
	assert.Equal(t, 0, n)
}

func TestWebhookStoresUnknownButIssueShapedEvent(t *testing.T) {
	server := newServer(t)
	client := server.Client()

	// Unrecognized event type with extra fields, but issue-shaped: stored.
	body := []byte(`{"timestamp":1785000000000,"webhookEvent":"jira:issue_worklogged","changelist":[],"issue":{"id":77,"key":"OPS-77","fields":{"summary":"Only summary","extra":true},"worklog":{"timeSpent":"1h"}}}`)
	code, payload := do(t, client, signedRequest(t, http.MethodPost, server.URL+"/api/v1/webhooks/jira", body, "delivery-77"))
	require.Equal(t, http.StatusAccepted, code, "payload: %v", payload)
	assert.Equal(t, "created", payload["outcome"])

	code, payload = do(t, client, signedRequest(t, http.MethodGet, server.URL+"/api/v1/incidents", nil, ""))
	require.Equal(t, http.StatusOK, code)
	incidents, ok := payload["incidents"].([]any)
	require.True(t, ok)
	require.Len(t, incidents, 1)
	assert.Equal(t, "OPS-77", incidents[0].(map[string]any)["external_id"])
}

func TestIncidentListFilterValidation(t *testing.T) {
	server := newServer(t)
	client := server.Client()

	// Unknown status filter -> 400.
	code, _ := do(t, client, signedRequest(t, http.MethodGet, server.URL+"/api/v1/incidents?status=bogus", nil, ""))
	assert.Equal(t, http.StatusBadRequest, code)

	// Bad limit -> 400; bad offset -> 400; valid offset -> 200.
	code, _ = do(t, client, signedRequest(t, http.MethodGet, server.URL+"/api/v1/incidents?limit=1000", nil, ""))
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = do(t, client, signedRequest(t, http.MethodGet, server.URL+"/api/v1/incidents?offset=-1", nil, ""))
	assert.Equal(t, http.StatusBadRequest, code)
	code, _ = do(t, client, signedRequest(t, http.MethodGet, server.URL+"/api/v1/incidents?offset=1", nil, ""))
	assert.Equal(t, http.StatusOK, code)

	// Valid filter on an empty table -> empty page, not null.
	code, payload := do(t, client, signedRequest(t, http.MethodGet, server.URL+"/api/v1/incidents?status=received", nil, ""))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, float64(0), payload["count"])
	incidents, ok := payload["incidents"].([]any)
	require.True(t, ok, "incidents must be an array even when empty")
	assert.Empty(t, incidents)
}
