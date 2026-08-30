package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
)

// DefaultBreakerThreshold and DefaultBreakerCooldown tune the primary
// classifier's breaker: three consecutive failures trips it, and one minute
// later a single probe retries the LLM.
const (
	DefaultBreakerThreshold = 3
	DefaultBreakerCooldown  = 60 * time.Second
)

// ResilientClassifier is the always-on classification path: LLM first, then
// the deterministic heuristic whenever the LLM fails or the breaker is open.
// It implements domain.Classifier.
type ResilientClassifier struct {
	primary  domain.Classifier
	fallback domain.Classifier
	breaker  *CircuitBreaker
}

// NewResilientClassifier wires primary → fallback behind a breaker.
func NewResilientClassifier(primary, fallback domain.Classifier, breaker *CircuitBreaker) *ResilientClassifier {
	return &ResilientClassifier{primary: primary, fallback: fallback, breaker: breaker}
}

// NewDefaultResilientClassifier builds the production shape: OpenAI primary,
// heuristic fallback, default breaker tuning.
func NewDefaultResilientClassifier(cfg OpenAIConfig) *ResilientClassifier {
	return NewResilientClassifier(
		NewOpenAIClassifier(cfg),
		NewHeuristicClassifier(),
		NewCircuitBreaker(DefaultBreakerThreshold, DefaultBreakerCooldown),
	)
}

// Classify tries the LLM while the breaker allows it; any LLM failure falls
// back to the heuristic, which is the intended degraded mode (the stored
// verdict carries source=heuristic). Only when both fail does this return an
// error, which the queue turns into a retry.
func (r *ResilientClassifier) Classify(ctx context.Context, inc domain.Incident) (*domain.Classification, error) {
	if r.breaker.Allow() {
		cls, err := r.primary.Classify(ctx, inc)
		if err == nil {
			r.breaker.RecordSuccess()
			return cls, nil
		}
		r.breaker.RecordFailure()
	}

	cls, err := r.fallback.Classify(ctx, inc)
	if err != nil {
		return nil, fmt.Errorf("llm and heuristic both failed: heuristic: %w", err)
	}
	return cls, nil
}
