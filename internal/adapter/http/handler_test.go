package httpadapter_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	httpadapter "github.com/ntttrang/ai-incident-triage/internal/adapter/http"
	"github.com/ntttrang/ai-incident-triage/internal/platform/logger"
	"github.com/ntttrang/ai-incident-triage/internal/platform/metrics"
)

func setupRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	return httpadapter.NewRouter(httpadapter.Dependencies{
		Log:     logger.New("error"),
		Metrics: metrics.New(),
		Health:  httpadapter.NewHealthHandler(nil),
		Env:     "test",
	})
}

func TestHealthz(t *testing.T) {
	r := setupRouter(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMetricsEndpoint(t *testing.T) {
	r := setupRouter(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUnknownRouteReturns404(t *testing.T) {
	r := setupRouter(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/nope", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
