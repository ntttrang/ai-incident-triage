package llm

import (
	"sync"
	"time"
)

// CircuitBreaker tracks consecutive primary-classifier failures. After
// threshold failures it opens for a cooldown, then half-opens: exactly one
// probe request is allowed through, and its outcome closes or re-opens the
// breaker. No external dependency — this is the whole state machine.
type CircuitBreaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	now       func() time.Time

	failures int
	openedAt time.Time
	probing  bool
}

// NewCircuitBreaker opens after threshold consecutive failures and cools down
// for the given duration before allowing a probe.
func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		threshold: threshold,
		cooldown:  cooldown,
		now:       time.Now,
	}
}

// SetNow overrides the clock; tests use it to advance past the cooldown
// without sleeping.
func (b *CircuitBreaker) SetNow(now func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.now = now
}

// Allow reports whether the primary may be called, and claims the half-open
// probe slot when the cooldown has elapsed.
func (b *CircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.openedAt.IsZero() {
		return true // closed
	}
	if b.now().Sub(b.openedAt) < b.cooldown {
		return false // open
	}
	if b.probing {
		return false // half-open: one probe already in flight
	}
	b.probing = true
	return true
}

// RecordSuccess closes the breaker after a successful call (including a
// successful half-open probe).
func (b *CircuitBreaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.openedAt = time.Time{}
	b.probing = false
}

// RecordFailure counts a failure and opens the breaker once the threshold of
// consecutive failures is reached.
func (b *CircuitBreaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.probing = false
	if b.openedAt.IsZero() && b.failures >= b.threshold {
		b.openedAt = b.now()
	} else if !b.openedAt.IsZero() {
		// a failed probe re-opens for another cooldown
		b.openedAt = b.now()
	}
}

// State exposes the breaker position for logging and tests.
func (b *CircuitBreaker) State() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openedAt.IsZero() {
		return "closed"
	}
	if b.now().Sub(b.openedAt) >= b.cooldown {
		return "half-open"
	}
	return "open"
}
