package tenant

import (
	"context"
	"fmt"
	"testing"

	"nudgebee/services/audit"
	"nudgebee/services/common"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tenantAccountExistsQuery = "SELECT EXISTS (SELECT 1 FROM cloud_accounts WHERE id = $1::uuid AND tenant = $2::uuid)"

// validate_access must refuse an account id that is not in the request's tenant,
// whatever role the user holds there. Every SaaS self-signup is tenant_admin of
// its own tenant, so the tenant shortcuts used to let any signup pass the
// relay/grafana proxy check for another customer's account.
func TestValidateAccess_AccountMustBelongToTenant(t *testing.T) {
	const (
		tenantId = "00000000-0000-0000-0000-0000000000a1"
		own      = "00000000-0000-0000-0000-0000000000b1"
		fresh    = "00000000-0000-0000-0000-0000000000b2" // in the tenant, not yet in the cached list
		foreign  = "00000000-0000-0000-0000-0000000000c1" // another tenant's account
	)
	// Keep the K8s RBAC branch off without a feature_flag query.
	require.NoError(t, featureFlagCache.Set(context.Background(), FEATURE_RBACK_K8S_ACCESS+":"+tenantId, []byte("false")))

	cases := []struct {
		name       string
		context    string // wire fields after TenantId/UserId/AccountIds
		accountId  string
		category   string
		permission security.SecurityAccessType
		dbInTenant *bool // nil: no ownership query expected (account is in the cached list, or empty)
		allowed    bool
	}{
		{name: "tenant admin, own account", context: `"Roles":["tenant_admin"]`, accountId: own, category: "ACCOUNTS", permission: "read", allowed: true},
		{name: "tenant admin, other tenant's account", context: `"Roles":["tenant_admin"]`, accountId: foreign, category: "ACCOUNTS", permission: "read", dbInTenant: ptr(false), allowed: false},
		{name: "tenant admin, other tenant's account, write", context: `"Roles":["tenant_admin"]`, accountId: foreign, category: "TICKETS", permission: "update", dbInTenant: ptr(false), allowed: false},
		{name: "tenant admin, non-uuid account id", context: `"Roles":["tenant_admin"]`, accountId: "not-a-uuid", category: "ACCOUNTS", permission: "read", allowed: false},
		{name: "tenant admin, account onboarded after the context was cached", context: `"Roles":["tenant_admin"]`, accountId: fresh, category: "ACCOUNTS", permission: "read", dbInTenant: ptr(true), allowed: true},
		{name: "read admin, own account", context: `"Roles":["tenant_admin_readonly"]`, accountId: own, category: "ACCOUNTS", permission: "read", allowed: true},
		{name: "read admin, other tenant's account", context: `"Roles":["tenant_admin_readonly"]`, accountId: foreign, category: "ACCOUNTS", permission: "read", dbInTenant: ptr(false), allowed: false},
		{name: "account admin, other tenant's account", context: `"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["` + own + `"]}`, accountId: foreign, category: "ACCOUNTS", permission: "read", dbInTenant: ptr(false), allowed: false},
		{name: "tenant-wide tickets grant, other tenant's account", context: `"Roles":[],"CustomPermissions":{"tickets:Write":true}`, accountId: foreign, category: "TICKETS", permission: "read", dbInTenant: ptr(false), allowed: false},
		{name: "tenant-wide tickets grant, own account", context: `"Roles":[],"CustomPermissions":{"tickets:Write":true}`, accountId: own, category: "TICKETS", permission: "read", allowed: true},
		// An empty account id is a tenant-level question and behaves as before.
		{name: "tenant admin, no account", context: `"Roles":["tenant_admin"]`, accountId: "", category: "TICKETS", permission: "read", allowed: true},
		{name: "read admin, no account, read", context: `"Roles":["tenant_admin_readonly"]`, accountId: "", category: "TICKETS", permission: "read", allowed: true},
		{name: "read admin, no account, write", context: `"Roles":["tenant_admin_readonly"]`, accountId: "", category: "TICKETS", permission: "update", allowed: false},
		{name: "no role, no account", context: `"Roles":[]`, accountId: "", category: "TICKETS", permission: "read", allowed: false},
	}

	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userId := fmt.Sprintf("user-validate-access-%d", i)
			wire := `{"TenantId":"` + tenantId + `","UserId":"` + userId + `","AccountIds":["` + own + `"],` + tc.context + `}`
			require.NoError(t, common.CacheSet("security_context_v3", tenantId+":"+userId, []byte(wire)))
			t.Cleanup(func() { _ = common.CacheDelete("security_context_v3", tenantId+":"+userId) })

			if tc.dbInTenant != nil {
				ffMockDB.ExpectQuery(tenantAccountExistsQuery).
					WithArgs(tc.accountId, tenantId).
					WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(*tc.dbInTenant))
			}

			req := ValidateAccessRequest{UserId: userId}
			req.Access = append(req.Access, struct {
				TenantId   string                      `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
				Permission security.SecurityAccessType `json:"permission" mapstructure:"permission" validate:"required"`
				Category   audit.EventCategory         `json:"category" mapstructure:"category" validate:"required"`
				Args       ValidateAccessRequestArgs   `json:"args" mapstructure:"args" validate:"required"`
			}{TenantId: tenantId, Permission: tc.permission, Category: audit.EventCategory(tc.category), Args: ValidateAccessRequestArgs{AccountId: tc.accountId}})

			resp, err := ValidateAccess(req)
			require.NoError(t, err)
			require.Len(t, resp.Access, 1)
			assert.Equal(t, tc.allowed, resp.Access[0].Allowed, resp.Access[0].Message)
			require.NoError(t, ffMockDB.ExpectationsWereMet())
		})
	}
}

func ptr(b bool) *bool { return &b }
