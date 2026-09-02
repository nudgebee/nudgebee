package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"nudgebee/llm/security"

	"github.com/gin-gonic/gin"
)

// Regression for #37020. /v1/budget/config/* is tenant-scoped for EVERY caller,
// super admins included: their session still carries the tenant they are
// viewing, and the Usage & Limits screen reads usage back for that tenant only.
// Before the fix a super admin skipped scoping outright, so the screen listed —
// and let them edit — other tenants' budget rows, which then showed the new
// value in the config list while the usage section kept the old one.
func TestRequireBudgetAdminTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		wire       string
		wantTenant string
		wantStatus int
	}{
		{
			name:       "super admin is pinned to the tenant its session carries",
			wire:       `{"TenantId":"t1","UserId":"u1","Roles":["` + security.AUTH_SUPER_ADMIN_FULL_ROLE + `"]}`,
			wantTenant: "t1",
		},
		{
			name:       "tenant admin is pinned to its own tenant",
			wire:       `{"TenantId":"t1","UserId":"u2","Roles":["` + security.AUTH_TENANT_ADMIN_ROLE + `"]}`,
			wantTenant: "t1",
		},
		{
			name:       "super admin with no tenant in the session is refused, not unscoped",
			wire:       `{"TenantId":"","UserId":"u3","Roles":["` + security.AUTH_SUPER_ADMIN_FULL_ROLE + `"]}`,
			wantStatus: 400,
		},
		{
			name:       "non-admin is refused",
			wire:       `{"TenantId":"t1","UserId":"u4","Roles":[]}`,
			wantStatus: 403,
		},
		{
			name:       "read-only super admin is not a budget admin",
			wire:       `{"TenantId":"t1","UserId":"u5","Roles":["` + security.AUTH_SUPER_ADMIN_READONLY_ROLE + `"]}`,
			wantStatus: 403,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			got := requireBudgetAdminTenant(c, authzContext(t, tt.wire))

			if got != tt.wantTenant {
				t.Errorf("tenant = %q, want %q", got, tt.wantTenant)
			}
			if tt.wantTenant != "" {
				if rec.Code != 200 {
					t.Errorf("wrote status %d on the allow path", rec.Code)
				}
				return
			}
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response body is not the { message } shape RPC actions expect: %v", err)
			}
			if body["message"] == "" {
				t.Error("error response carries no message")
			}
		})
	}
}
