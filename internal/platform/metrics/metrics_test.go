package metrics_test

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ntttrang/ai-incident-triage/internal/platform/metrics"
)

// The nil-receiver guards keep constructors' observer argument optional;
// every observe method must tolerate a nil Metrics so callers can pass nil
// in tests without nil checks of their own.
func TestObserveMethodsAreNilSafe(t *testing.T) {
	var m *metrics.Metrics

	assert.NotPanics(t, func() { m.ObserveIngest("created") })
	assert.NotPanics(t, func() { m.ObserveClassification("llm", "high", time.Second) })
	assert.NotPanics(t, func() { m.ObserveClassificationFailure() })
	assert.NotPanics(t, func() { m.ObserveLLMRequest("gpt-5-chat-latest", time.Second, true) })
	assert.NotPanics(t, func() { m.ObserveLLMTokens("gpt-5-chat-latest", "prompt", 10) })
	assert.NotPanics(t, func() { m.ObserveFallbackTrip() })
	assert.NotPanics(t, func() { m.ObserveDBPool(nil) })
}

func TestObserveLLMRequestRecordsOutcome(t *testing.T) {
	m := metrics.New()

	m.ObserveLLMRequest("test-model", 2*time.Second, false)
	m.ObserveLLMRequest("test-model", 60*time.Second, true)

	// The failing tail must be separable: failures land in their own bucket
	// series, not averaged into the successes.
	assert.EqualValues(t, 1, histogramSampleCount(t, m.LLMRequestDuration, "success"))
	assert.EqualValues(t, 1, histogramSampleCount(t, m.LLMRequestDuration, "failed"))
	assert.EqualValues(t, 0, histogramSampleCount(t, m.LLMRequestDuration, "other"))
}

func TestNewBuildsIndependentRegistries(t *testing.T) {
	a := metrics.New()
	b := metrics.New()

	a.ObserveIngest("created")

	assert.Equal(t, 1.0, testutil.ToFloat64(a.IncidentsReceived.WithLabelValues("created")))
	assert.Equal(t, 0.0, testutil.ToFloat64(b.IncidentsReceived.WithLabelValues("created")),
		"each process owns its registry; a second New must start from zero")
}

// histogramSampleCount reads the observed-sample count of one outcome series
// in a HistogramVec; testutil.ToFloat64 covers counters and gauges only.
func histogramSampleCount(t *testing.T, vec *prometheus.HistogramVec, outcome string) uint64 {
	t.Helper()

	reg := prometheus.NewRegistry()
	reg.MustRegister(vec)
	families, err := reg.Gather()
	require.NoError(t, err)
	require.Len(t, families, 1)

	for _, metric := range families[0].GetMetric() {
		for _, lp := range metric.GetLabel() {
			if lp.GetName() == "outcome" && lp.GetValue() == outcome {
				require.NotNil(t, metric.GetHistogram(), "collector is not a histogram")
				return metric.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}
