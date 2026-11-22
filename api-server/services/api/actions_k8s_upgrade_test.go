package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"nudgebee/services/k8s_upgrade"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
)

func TestAuthorizeUpgradeAccountDeniesForeignAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// A tenant admin of t1 holds no access to an account outside t1's account list.
	sc := security.NewSecurityContextForTenantAdmin("t1")
	ctx := security.NewRequestContext(context.Background(), sc, nil, nil, nil)

	for _, access := range []security.SecurityAccessType{security.SecurityAccessTypeRead, security.SecurityAccessTypeUpdate} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		if authorizeUpgradeAccount(ctx, c, "foreign-account", access) {
			t.Fatalf("access %v to a foreign account was allowed", access)
		}
		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", w.Code)
		}
	}
}

func TestAuthorizeUpgradeAccountFailsClosedWithoutContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, ctx := range map[string]*security.RequestContext{
		"nil request context":  nil,
		"nil security context": security.NewRequestContext(context.Background(), nil, nil, nil, nil),
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		if authorizeUpgradeAccount(ctx, c, "any-account", security.SecurityAccessTypeRead) {
			t.Fatalf("%s: access was allowed", name)
		}
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", name, w.Code)
		}
	}
}

func TestUpgradeErrorMapsNotFoundTo404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	upgradeError(c, errors.Join(errors.New("task x"), k8s_upgrade.ErrNotFound))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}

	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	upgradeError(c, errors.New("boom"))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
