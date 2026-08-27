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

// Liveness godoc
// @Summary Liveness probe
// @Tags health
// @Produce json
// @Success 200 {object} HealthResponse
// @Router /healthz [get]
func (h *HealthHandler) Liveness(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{Status: "ok"})
}

// Readiness godoc
// @Summary Readiness probe
// @Tags health
// @Produce json
// @Success 200 {object} HealthResponse
// @Failure 503 {object} ErrorResponse
// @Router /readyz [get]
func (h *HealthHandler) Readiness(c *gin.Context) {
	if err := h.pool.Ping(c.Request.Context()); err != nil {
		c.JSON(http.StatusServiceUnavailable, ErrorResponse{Error: "database unavailable"})
		return
	}
	c.JSON(http.StatusOK, HealthResponse{Status: "ready"})
}
