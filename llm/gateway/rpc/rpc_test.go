package rpc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func ctxWithRole(role string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	if role != "" {
		c.Request.Header.Set("x-user-role", role)
	}
	return c
}

func TestCallerRole(t *testing.T) {
	assert.Equal(t, "tenant_admin", CallerRole(ctxWithRole("tenant_admin")))
	assert.Equal(t, "", CallerRole(ctxWithRole("")))
}

func TestTenantRoleGates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	run := func(h gin.HandlerFunc, roles, label string, setRoles bool) int {
		r := gin.New()
		r.POST("/x", h, func(c *gin.Context) { c.Status(http.StatusOK) })
		req := httptest.NewRequest(http.MethodPost, "/x", nil)
		if setRoles {
			req.Header.Set("x-user-roles", roles)
		}
		if label != "" {
			req.Header.Set("x-user-role", label)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	for _, tc := range []struct {
		name, roles, label string
		setRoles           bool
		manager, viewer    int
	}{
		{"tenant admin", "tenant_admin", "tenant_admin", true, 200, 200},
		{"tenant admin with account role", "account_admin,tenant_admin", "tenant_admin", true, 200, 200},
		{"read-only tenant admin", "tenant_admin_readonly", "tenant_admin_readonly", true, 403, 200},
		{"case and spaces normalised", " Tenant_Admin_Readonly ", "", true, 403, 200},
		{"account admin read-only", "account_admin_readonly", "account_admin_readonly", true, 403, 403},
		{"k8s namespace admin read-only", "k8s_namespace_admin_readonly", "", true, 403, 403},
		{"super admin, not a member", "tenant_admin_readonly,super_admin", "tenant_admin_readonly", true, 200, 200},
		{"custom-role user (grants only)", "", "tenant_admin_readonly", true, 403, 403},
		{"custom-role user, no-roles sentinel", "-", "tenant_admin_readonly", true, 403, 403},
		{"account admin", "account_admin", "account_admin", true, 403, 403},
		{"k8s namespace admin", "k8s_namespace_admin", "k8s_namespace_admin", true, 403, 403},
		{"old app pod: label only", "", "tenant_admin", false, 200, 200},
		{"old app pod: label only, account admin", "", "account_admin", false, 403, 403},
		{"service call", "", "", false, 200, 200},
	} {
		assert.Equal(t, tc.manager, run(RequireTenantManager(), tc.roles, tc.label, tc.setRoles), "manager: "+tc.name)
		assert.Equal(t, tc.viewer, run(RequireTenantViewer(), tc.roles, tc.label, tc.setRoles), "viewer: "+tc.name)
	}
}

func TestCallerRolesDropsSentinel(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
	c.Request.Header.Set("x-user-roles", " - ")
	roles, ok := callerRoles(c)
	assert.True(t, ok)
	assert.Empty(t, roles)
}
