package metrics

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics holds Prometheus collectors for the application.
type Metrics struct {
	HTTPRequestsTotal      *prometheus.CounterVec
	HTTPRequestDuration    *prometheus.HistogramVec
	ClassificationTotal    *prometheus.CounterVec
	ClassificationDuration *prometheus.HistogramVec
	IncidentsReceived      *prometheus.CounterVec
	LLMRequestDuration     *prometheus.HistogramVec
	LLMTokensTotal         *prometheus.CounterVec
	LLMFallbackTrips       prometheus.Counter
	ClassifyJobsFailed     prometheus.Counter
	DBPoolAcquired         prometheus.Gauge
	DBPoolIdle             prometheus.Gauge
	DBPoolTotal            prometheus.Gauge
	DBPoolMax              prometheus.Gauge
	Registry               *prometheus.Registry
}

// New registers and returns application metrics on a dedicated registry.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	m := &Metrics{
		Registry: reg,
		HTTPRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total number of HTTP requests",
			},
			[]string{"method", "path", "status"},
		),
		HTTPRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request duration in seconds",
				Buckets: prometheus.DefBuckets,
			},
			[]string{"method", "path"},
		),
		DBPoolAcquired: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "db_pool_acquired_connections",
			Help: "Number of currently acquired database connections",
		}),
		DBPoolIdle: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "db_pool_idle_connections",
			Help: "Number of idle database connections",
		}),
		DBPoolTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "db_pool_total_connections",
			Help: "Total number of database connections in the pool",
		}),
		DBPoolMax: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "db_pool_max_connections",
			Help: "Maximum number of database connections allowed",
		}),
		ClassificationTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "classification_total",
				Help: "Classification attempts by source (llm|heuristic) and severity",
			},
			[]string{"source", "severity"},
		),
		ClassificationDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "classification_duration_seconds",
				Help:    "Classification wall-clock duration by source",
				Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2.5, 5, 10, 30, 60, 120},
			},
			[]string{"source"},
		),
		IncidentsReceived: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "incidents_received_total",
				Help: "Webhook deliveries accepted for ingest by outcome (created|updated|duplicate)",
			},
			[]string{"outcome"},
		),
		LLMRequestDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "llm_request_duration_seconds",
				Help:    "OpenAI request latency by model and outcome — the timeout story needs the failing tail",
				Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60},
			},
			[]string{"model", "outcome"},
		),
		LLMTokensTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "llm_tokens_total",
				Help: "Tokens consumed by the classifier by model and type (prompt|completion)",
			},
			[]string{"model", "type"},
		),
		LLMFallbackTrips: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "llm_fallback_trips_total",
				Help: "Classifications served by the heuristic fallback after an LLM failure",
			},
		),
		ClassifyJobsFailed: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "classify_jobs_failed_total",
				Help: "Incidents marked failed after classification exhausted its retries",
			},
		),
	}

	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.DBPoolAcquired,
		m.DBPoolIdle,
		m.DBPoolTotal,
		m.DBPoolMax,
		m.ClassificationTotal,
		m.ClassificationDuration,
		m.IncidentsReceived,
		m.LLMRequestDuration,
		m.LLMTokensTotal,
		m.LLMFallbackTrips,
		m.ClassifyJobsFailed,
	)
	return m
}

// ObserveClassification implements service.ClassifyObserver.
func (m *Metrics) ObserveClassification(source string, severity string, duration time.Duration) {
	if m == nil {
		return
	}
	m.ClassificationTotal.WithLabelValues(source, severity).Inc()
	m.ClassificationDuration.WithLabelValues(source).Observe(duration.Seconds())
}

// ObserveClassificationFailure implements the extended service.ClassifyObserver.
func (m *Metrics) ObserveClassificationFailure() {
	if m == nil {
		return
	}
	m.ClassifyJobsFailed.Inc()
}

// ObserveIngest implements the http adapter's IngestObserver.
func (m *Metrics) ObserveIngest(outcome string) {
	if m == nil {
		return
	}
	m.IncidentsReceived.WithLabelValues(outcome).Inc()
}

// ObserveLLMRequest implements llm.Observer: request latency by model and
// outcome — the timeout story needs the failing tail.
func (m *Metrics) ObserveLLMRequest(model string, duration time.Duration, failed bool) {
	if m == nil {
		return
	}
	outcome := "success"
	if failed {
		outcome = "failed"
	}
	m.LLMRequestDuration.WithLabelValues(model, outcome).Observe(duration.Seconds())
}

// ObserveLLMTokens implements llm.Observer.
func (m *Metrics) ObserveLLMTokens(model, tokenType string, count int64) {
	if m == nil {
		return
	}
	m.LLMTokensTotal.WithLabelValues(model, tokenType).Add(float64(count))
}

// ObserveFallbackTrip implements llm.Observer.
func (m *Metrics) ObserveFallbackTrip() {
	if m == nil {
		return
	}
	m.LLMFallbackTrips.Inc()
}

// ObserveDBPool updates gauges from a pgx pool snapshot.
func (m *Metrics) ObserveDBPool(stat *pgxpool.Stat) {
	if m == nil || stat == nil {
		return
	}
	m.DBPoolAcquired.Set(float64(stat.AcquiredConns()))
	m.DBPoolIdle.Set(float64(stat.IdleConns()))
	m.DBPoolTotal.Set(float64(stat.TotalConns()))
	m.DBPoolMax.Set(float64(stat.MaxConns()))
}
