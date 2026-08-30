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
