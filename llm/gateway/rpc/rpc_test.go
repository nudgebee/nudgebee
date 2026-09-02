package rpc

import (
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

func TestIsTenantAdmin(t *testing.T) {
	// Admin roles (incl. read-only) grant tenant-wide reads.
	for _, r := range []string{"tenant_admin", "tenant_admin_readonly", "account_admin", "account_admin_readonly", "super_admin", "super_admin_readonly"} {
		assert.True(t, IsTenantAdmin(ctxWithRole(r)), r)
	}
	// Non-admin / unknown / empty do not.
	for _, r := range []string{"user", "viewer", "k8s_namespace_readonly", ""} {
		assert.False(t, IsTenantAdmin(ctxWithRole(r)), r)
	}
	assert.Equal(t, "tenant_admin", CallerRole(ctxWithRole("tenant_admin")))
	assert.Equal(t, "", CallerRole(ctxWithRole("")))
}

func TestRequireWriteRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	run := func(role string) (int, bool) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/", nil)
		if role != "" {
			c.Request.Header.Set("x-user-role", role)
		}
		reached := false
		mw := RequireWriteRole()
		mw(c)
		if !c.IsAborted() {
			reached = true
		}
		return rec.Code, reached
	}

	// Read-only roles are refused: the config plane is otherwise guarded only by
	// the shared service token, and the app grants llm_gateway_* to every built-in
	// role — so this middleware is the only thing standing between a
	// *_readonly caller and the tenant's routing rules / quotas / tier mappings.
	for _, r := range []string{"tenant_admin_readonly", "account_admin_readonly", "k8s_namespace_admin_readonly", "super_admin_readonly"} {
		code, reached := run(r)
		assert.False(t, reached, r)
		assert.Equal(t, 403, code, r)
	}

	// Write-capable roles pass through.
	for _, r := range []string{"tenant_admin", "account_admin", "k8s_namespace_admin", "super_admin"} {
		_, reached := run(r)
		assert.True(t, reached, r)
	}

	// No role header = direct service-to-service call (already service-token
	// gated), so it is NOT treated as read-only.
	_, reached := run("")
	assert.True(t, reached)
}

func TestRequireWriteRoleNormalizesHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reached := func(role string) bool {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/", nil)
		c.Request.Header.Set("x-user-role", role)
		RequireWriteRole()(c)
		return !c.IsAborted()
	}

	// Case and surrounding whitespace must not let a read-only role through:
	// the header is attacker-visible in the sense that any deviation from the
	// canonical spelling would otherwise miss the map and be read as write-capable.
	for _, r := range []string{
		"Tenant_Admin_Readonly",
		"TENANT_ADMIN_READONLY",
		"  account_admin_readonly  ",
		"\tk8s_namespace_admin_readonly\n",
	} {
		assert.False(t, reached(r), r)
	}

	// Normalization must not start refusing write-capable roles.
	assert.True(t, reached("Tenant_Admin"))
	assert.True(t, reached(" account_admin "))
}
