package metrics

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics holds Prometheus collectors for the application.
type Metrics struct {
	HTTPRequestsTotal   *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec
	AuthLoginTotal      *prometheus.CounterVec
	UserOperationsTotal *prometheus.CounterVec
	DBPoolAcquired      prometheus.Gauge
	DBPoolIdle          prometheus.Gauge
	DBPoolTotal         prometheus.Gauge
	DBPoolMax           prometheus.Gauge
	Registry            *prometheus.Registry
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
		AuthLoginTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "auth_login_total",
				Help: "Total login attempts by result",
			},
			[]string{"result"},
		),
		UserOperationsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "user_operations_total",
				Help: "Total user operations by operation and result",
			},
			[]string{"op", "result"},
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
	}

	reg.MustRegister(
		m.HTTPRequestsTotal,
		m.HTTPRequestDuration,
		m.AuthLoginTotal,
		m.UserOperationsTotal,
		m.DBPoolAcquired,
		m.DBPoolIdle,
		m.DBPoolTotal,
		m.DBPoolMax,
	)
	return m
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

// IncAuthLogin increments the auth login counter.
func (m *Metrics) IncAuthLogin(result string) {
	if m == nil {
		return
	}
	m.AuthLoginTotal.WithLabelValues(result).Inc()
}

// IncUserOp increments the user operations counter.
func (m *Metrics) IncUserOp(op, result string) {
	if m == nil {
		return
	}
	m.UserOperationsTotal.WithLabelValues(op, result).Inc()
}
