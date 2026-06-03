package license

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestLicenseMeOSSPath proves the /v1/license/me handler boots and serves
// correct OSS-mode output without any EE init() having registered. This is
// the runtime smoke test the OSS snapshot would otherwise lack: it asserts
// that the default OSS impl flows end-to-end through the handler.
func TestLicenseMeOSSPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/v1/license/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got, want := body["tier"], "oss"; got != want {
		t.Errorf("tier = %v, want %v", got, want)
	}
	if got, want := body["status"], "missing"; got != want {
		t.Errorf("status = %v, want %v", got, want)
	}
	if body["features"] != nil {
		t.Errorf("features = %v, want nil/empty", body["features"])
	}
	if got, want := body["tenant_id"], ""; got != want {
		t.Errorf("tenant_id = %v, want %v", got, want)
	}
}

// TestBootstrapCheckOSSAllowsAnyEmail verifies the OSS-mode bootstrap path:
// no license email is configured, so any signin email is allowed and gets
// an empty tenant_id (signaling "OSS auto-bootstrap" to the caller).
func TestBootstrapCheckOSSAllowsAnyEmail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/v1/license/bootstrap-check?email=anyone%40example.com", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got, want := body["allowed"], true; got != want {
		t.Errorf("allowed = %v, want %v", got, want)
	}
	if got, want := body["tenant_id"], ""; got != want {
		t.Errorf("tenant_id = %v, want %v", got, want)
	}
	if got, want := body["role"], "tenant_admin"; got != want {
		t.Errorf("role = %v, want %v", got, want)
	}
}
