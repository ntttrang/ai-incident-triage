package httpadapter

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/ntttrang/ai-incident-triage/internal/domain"
	"github.com/ntttrang/ai-incident-triage/internal/platform/logger"
)

func TestMapError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"not found", domain.ErrNotFound, http.StatusNotFound, "not found"},
		{"conflict", domain.ErrConflict, http.StatusConflict, "conflict"},
		{"invalid input", domain.ErrInvalidInput, http.StatusBadRequest, "invalid input"},
		{"internal", errors.New("boom"), http.StatusInternalServerError, "internal server error"},
		{"wrapped sentinel", fmt.Errorf("find incident: %w", domain.ErrNotFound), http.StatusNotFound, "not found"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/incidents/abc", nil)

			mapError(c, logger.New("error"), tc.err)

			assert.Equal(t, tc.status, w.Code)
			var resp ErrorResponse
			assert.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, tc.body, resp.Error)
			assert.NotContains(t, w.Body.String(), "boom", "internals must not leak")
		})
	}
}
