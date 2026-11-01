package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func newCapabilityCatalogRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Nil is fine — both tests return before tracer/meter are touched.
	var tracer trace.Tracer
	var meter metric.Meter
	handleCapabilityCatalogApi(r, tracer, meter)
	return r
}

// Account-scoping is verified live (see PR description). This covers only
// request validation: rejecting a missing account_id before doing any work.
func TestCapabilityCatalog_MissingAccountId(t *testing.T) {
	r := newCapabilityCatalogRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/capabilities/catalog", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "account_id is required")
}

func TestCapabilityCatalog_MalformedBody(t *testing.T) {
	r := newCapabilityCatalogRouter()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/capabilities/catalog", strings.NewReader(`not json`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
