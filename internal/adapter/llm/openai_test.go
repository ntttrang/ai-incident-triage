package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/adapter/llm"
	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// stubCompletion is the Chat Completions envelope with a strict-schema shaped
// content payload.
func stubCompletion(t *testing.T, category, severity string) []byte {
	t.Helper()
	content, err := json.Marshal(map[string]any{
		"category":       category,
		"severity":       severity,
		"priority_score": 80,
		"confidence":     0.9,
		"rationale":      "summary mentions a database outage",
	})
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"content": string(content)}},
		},
	})
	require.NoError(t, err)
	return body
}

func TestOpenAIClassifierParsesStructuredOutput(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(stubCompletion(t, "database", "critical"))
	}))
	t.Cleanup(server.Close)

	c := llm.NewOpenAIClassifier(llm.OpenAIConfig{BaseURL: server.URL, Timeout: 2 * time.Second})

	cls, err := c.Classify(context.Background(), domain.Incident{
		Summary: "Prod db down", Description: "primary replica unreachable", Labels: []string{"prod"},
	})
	require.NoError(t, err)

	assert.Equal(t, "/chat/completions", gotPath)
	assert.Equal(t, "database", cls.Category)
	assert.Equal(t, "critical", cls.Severity)
	assert.Equal(t, 80, cls.PriorityScore)
	assert.InEpsilon(t, 0.9, cls.Confidence, 0.001)
	assert.Equal(t, domain.ClassifySourceLLM, cls.Source)
}

func TestOpenAIClassifierSendsStrictSchemaAndPrompt(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(stubCompletion(t, "access", "low"))
	}))
	t.Cleanup(server.Close)

	c := llm.NewOpenAIClassifier(llm.OpenAIConfig{BaseURL: server.URL, Timeout: 2 * time.Second})
	_, err := c.Classify(context.Background(), domain.Incident{Summary: "cannot log in", IssueType: "Bug"})
	require.NoError(t, err)

	require.NotEmpty(t, payload["messages"], "request must carry prompt messages")
	format, ok := payload["response_format"].(map[string]any)
	require.True(t, ok, "request must request a response format")
	schemaWrapper, ok := format["json_schema"].(map[string]any)
	require.True(t, ok, "response format must be json_schema")
	strict, _ := schemaWrapper["strict"].(bool)
	assert.True(t, strict, "structured output must be strict")

	// The user prompt carries the incident projection.
	userMsg := payload["messages"].([]any)[1].(map[string]any)
	content := userMsg["content"].(string)
	assert.Contains(t, content, "cannot log in")
	assert.Contains(t, content, "Bug")
}

func TestOpenAIClassifierTimesOutAgainstSlowServer(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block // never answers in time
	}))
	t.Cleanup(func() { close(block); server.Close() })

	// The LLM budget is derived from the Work context; here it is deliberately
	// far shorter than the hang so the timeout path fires quickly.
	c := llm.NewOpenAIClassifier(llm.OpenAIConfig{BaseURL: server.URL, Timeout: 100 * time.Millisecond})

	_, err := c.Classify(context.Background(), domain.Incident{Summary: "db down"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

func TestOpenAIClassifierRejectsEmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	t.Cleanup(server.Close)

	c := llm.NewOpenAIClassifier(llm.OpenAIConfig{BaseURL: server.URL, Timeout: 2 * time.Second})
	_, err := c.Classify(context.Background(), domain.Incident{Summary: "s"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no choices")
}

// rawCompletion serves arbitrary content so tests can feed schema-valid but
// semantically invalid replies.
func rawCompletion(content string) []byte {
	inner, _ := json.Marshal(content)
	return []byte(`{"choices":[{"message":{"content":` + string(inner) + `}}]}`)
}

// The strict schema constrains types, not ranges or vocabulary; the classifier
// must reject those itself so the resilient chain degrades to the heuristic
// instead of persisting an unpersistable verdict.
func TestOpenAIClassifierRejectsOutOfRangeNumerics(t *testing.T) {
	for name, content := range map[string]string{
		"priority above 100":  `{"category":"access","severity":"low","priority_score":1000,"confidence":0.9,"rationale":"r"}`,
		"priority below 1":    `{"category":"access","severity":"low","priority_score":0,"confidence":0.9,"rationale":"r"}`,
		"confidence above 1":  `{"category":"access","severity":"low","priority_score":50,"confidence":1.7,"rationale":"r"}`,
		"confidence negative": `{"category":"access","severity":"low","priority_score":50,"confidence":-0.1,"rationale":"r"}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(rawCompletion(content))
			}))
			t.Cleanup(server.Close)

			c := llm.NewOpenAIClassifier(llm.OpenAIConfig{BaseURL: server.URL, Timeout: 2 * time.Second})
			_, err := c.Classify(context.Background(), domain.Incident{Summary: "s"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "outside")
		})
	}
}

func TestOpenAIClassifierRejectsOutsideVocabulary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(rawCompletion(
			`{"category":"hardware","severity":"low","priority_score":50,"confidence":0.9,"rationale":"r"}`))
	}))
	t.Cleanup(server.Close)

	c := llm.NewOpenAIClassifier(llm.OpenAIConfig{BaseURL: server.URL, Timeout: 2 * time.Second})
	_, err := c.Classify(context.Background(), domain.Incident{Summary: "s"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "vocabulary")
}
