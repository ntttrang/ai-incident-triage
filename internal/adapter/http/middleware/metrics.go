package middleware

import (
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ntttrang/ai-incident-triage/internal/platform/metrics"
)

// Metrics records Prometheus HTTP metrics.
func Metrics(m *metrics.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		rawPath := c.Request.URL.Path
		if strings.HasPrefix(rawPath, "/debug/pprof") {
			return
		}

		path := c.FullPath()
		if path == "" {
			path = "unknown"
		}
		if _, skip := noisyPaths[path]; skip {
			return
		}
		status := strconv.Itoa(c.Writer.Status())
		m.HTTPRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		m.HTTPRequestDuration.WithLabelValues(c.Request.Method, path).Observe(time.Since(start).Seconds())
	}
}
