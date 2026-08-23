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
