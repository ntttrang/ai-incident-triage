package middleware

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
)

// Recovery recovers from panics and returns 500.
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				log.ErrorContext(c.Request.Context(), "panic recovered",
					"error", rec,
					"path", c.Request.URL.Path,
					"request_id", c.GetString("request_id"),
				)
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
			}
		}()
		c.Next()
	}
}
