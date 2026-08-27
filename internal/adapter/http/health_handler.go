package httpadapter

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HealthHandler serves liveness and readiness probes.
type HealthHandler struct {
	pool *pgxpool.Pool
}

// NewHealthHandler creates a HealthHandler.
func NewHealthHandler(pool *pgxpool.Pool) *HealthHandler {
	return &HealthHandler{pool: pool}
}

// Liveness reports process health without dependencies.
func (h *HealthHandler) Liveness(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{Status: "ok"})
}

// Readiness reports whether the database connection answers pings.
func (h *HealthHandler) Readiness(c *gin.Context) {
	if err := h.pool.Ping(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{Error: "database unavailable"})
		return
	}
	c.JSON(http.StatusOK, HealthResponse{Status: "ready"})
}
