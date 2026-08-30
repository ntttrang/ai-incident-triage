package llm_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/adapter/llm"
	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

type stubClassifier struct {
	calls int
	cls   *domain.Classification
	err   error
}

func (s *stubClassifier) Classify(_ context.Context, _ domain.Incident) (*domain.Classification, error) {
	s.calls++
	return s.cls, s.err
}

func llmVerdict() *domain.Classification {
	return &domain.Classification{Category: "database", Severity: "high", Source: domain.ClassifySourceLLM}
}

func TestResilientPrimaryWins(t *testing.T) {
	primary := &stubClassifier{cls: llmVerdict()}
	fallback := &stubClassifier{}
	r := llm.NewResilientClassifier(primary, fallback, llm.NewCircuitBreaker(3, 0))

	cls, err := r.Classify(context.Background(), domain.Incident{Summary: "db down"})
	require.NoError(t, err)
	assert.Equal(t, domain.ClassifySourceLLM, cls.Source)
	assert.Equal(t, 1, primary.calls)
	assert.Zero(t, fallback.calls, "fallback must stay idle while the LLM is healthy")
}

func TestResilientFallsBackOnPrimaryFailure(t *testing.T) {
	primary := &stubClassifier{err: errors.New("openai 500")}
	fallback := llm.NewHeuristicClassifier()
	r := llm.NewResilientClassifier(primary, fallback, llm.NewCircuitBreaker(3, 0))

	cls, err := r.Classify(context.Background(), domain.Incident{Summary: "db down"})
	require.NoError(t, err, "degraded mode is a success, not an error")
	assert.Equal(t, domain.ClassifySourceHeuristic, cls.Source, "the verdict records which classifier answered")
}

func TestResilientFailsOnlyWhenBothFail(t *testing.T) {
	primary := &stubClassifier{err: errors.New("openai down")}
	fallback := &stubClassifier{err: errors.New("heuristic broken")}
	r := llm.NewResilientClassifier(primary, fallback, llm.NewCircuitBreaker(3, 0))

	_, err := r.Classify(context.Background(), domain.Incident{Summary: "db down"})
	require.Error(t, err, "both classifiers failing must surface as a retryable job error")
	assert.Contains(t, err.Error(), "heuristic")
}

func TestResilientSkipsPrimaryWhenBreakerOpen(t *testing.T) {
	primary := &stubClassifier{err: errors.New("openai 500")}
	fallback := llm.NewHeuristicClassifier()
	breaker := llm.NewCircuitBreaker(1, time.Hour) // long cooldown: stays open
	r := llm.NewResilientClassifier(primary, fallback, breaker)

	// Trip the breaker.
	_, err := r.Classify(context.Background(), domain.Incident{Summary: "db down"})
	require.NoError(t, err)
	callsAfterTrip := primary.calls

	cls, err := r.Classify(context.Background(), domain.Incident{Summary: "db down"})
	require.NoError(t, err)
	assert.Equal(t, callsAfterTrip, primary.calls, "an open breaker must not call the LLM at all")
	assert.Equal(t, domain.ClassifySourceHeuristic, cls.Source)
}
