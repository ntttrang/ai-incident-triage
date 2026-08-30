package llm_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/ntttrang/ai-incident-triage/internal/adapter/llm"
)

// newBreaker returns a breaker with a one-minute cooldown and a controllable
// clock starting at t0.
func newBreaker(threshold int) (*llm.CircuitBreaker, *time.Time) {
	t0 := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	now := t0
	b := llm.NewCircuitBreaker(threshold, time.Minute)
	b.SetNow(func() time.Time { return now })
	return b, &now
}

func TestBreakerClosedUntilThreshold(t *testing.T) {
	b, _ := newBreaker(3)

	assert.True(t, b.Allow())
	b.RecordFailure()
	b.RecordFailure()
	assert.True(t, b.Allow(), "two failures stay closed at threshold 3")
	assert.Equal(t, "closed", b.State())

	b.RecordFailure()
	assert.Equal(t, "open", b.State())
	assert.False(t, b.Allow(), "open breaker blocks the primary")
}

func TestBreakerHalfOpenAllowsSingleProbe(t *testing.T) {
	b, now := newBreaker(1)

	b.RecordFailure()
	assert.False(t, b.Allow())

	*now = now.Add(2 * time.Minute)
	assert.Equal(t, "half-open", b.State())
	assert.True(t, b.Allow(), "first call after cooldown claims the probe slot")
	assert.False(t, b.Allow(), "second concurrent call is blocked while probing")

	// The probe fails: the breaker re-opens for another full cooldown.
	b.RecordFailure()
	assert.Equal(t, "open", b.State())
	assert.False(t, b.Allow())
}

func TestBreakerProbeSuccessCloses(t *testing.T) {
	b, now := newBreaker(1)

	b.RecordFailure()
	*now = now.Add(time.Minute)
	assert.True(t, b.Allow(), "probe allowed after cooldown")

	b.RecordSuccess()
	assert.Equal(t, "closed", b.State())
	assert.True(t, b.Allow())
	assert.True(t, b.Allow(), "closed breaker allows every call")
}

func TestBreakerSuccessResetsConsecutiveCount(t *testing.T) {
	b, _ := newBreaker(3)

	b.RecordFailure()
	b.RecordFailure()
	b.RecordSuccess()
	b.RecordFailure()
	b.RecordFailure()
	assert.Equal(t, "closed", b.State(), "intermittent failures must not trip the breaker")
}
