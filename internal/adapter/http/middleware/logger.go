package middleware

import (
	"log/slog"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

var noisyPaths = map[string]struct{}{
	"/metrics": {},
	"/healthz": {},
	"/readyz":  {},
}

// RequestLogger logs each HTTP request with structured fields and trace correlation.
func RequestLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		if _, skip := noisyPaths[path]; skip || strings.HasPrefix(path, "/debug/pprof") {
			return
		}

		log.InfoContext(c.Request.Context(), "http request",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
			"request_id", c.GetString("request_id"),
		)
	}
}
