package httpadapter

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
	"github.com/ntttrang/ai-incident-triage/internal/service"
)

// IncidentHandler serves the incident read API. Neither endpoint returns the
// stored raw webhook body.
type IncidentHandler struct {
	svc *service.IncidentService
	log *slog.Logger
}

// NewIncidentHandler returns a read-API handler backed by the service.
func NewIncidentHandler(svc *service.IncidentService, log *slog.Logger) *IncidentHandler {
	return &IncidentHandler{svc: svc, log: log}
}

// List handles GET /api/v1/incidents?status=&severity=&limit=&offset=.
func (h *IncidentHandler) List(c *gin.Context) {
	status := c.Query("status")
	if status != "" && !domain.KnownStatus(domain.IncidentStatus(status)) {
		mapError(c, h.log, fmt.Errorf("unknown status filter %q: %w", status, domain.ErrInvalidInput))
		return
	}

	limit, err := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if err != nil || limit < 1 || limit > 100 {
		mapError(c, h.log, fmt.Errorf("limit must be an integer between 1 and 100: %w", domain.ErrInvalidInput))
		return
	}
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		mapError(c, h.log, fmt.Errorf("offset must be a non-negative integer: %w", domain.ErrInvalidInput))
		return
	}

	incidents, err := h.svc.ListIncidents(c.Request.Context(), domain.IncidentFilter{
		Status:   status,
		Severity: c.Query("severity"),
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		mapError(c, h.log, err)
		return
	}

	resp := IncidentListResponse{
		Incidents: make([]IncidentResponse, 0, len(incidents)),
		Count:     len(incidents),
	}
	for _, inc := range incidents {
		resp.Incidents = append(resp.Incidents, toIncidentResponse(inc))
	}
	c.JSON(http.StatusOK, resp)
}

// Get handles GET /api/v1/incidents/:id.
func (h *IncidentHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		mapError(c, h.log, fmt.Errorf("invalid incident id %q: %w", c.Param("id"), domain.ErrInvalidInput))
		return
	}

	inc, err := h.svc.GetIncident(c.Request.Context(), id)
	if err != nil {
		mapError(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, toIncidentResponse(*inc))
}
