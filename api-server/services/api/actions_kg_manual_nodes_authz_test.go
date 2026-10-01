package api

import (
	"encoding/json"
	"fmt"
	"testing"

	"nudgebee/services/security"
)

// An account admin writes declarations only in their own accounts; one outside
// any account belongs to the tenant, so it needs tenant-wide access.
func TestManualNodeAuthorizer(t *testing.T) {
	ctxFor := func(role string, scoped []string) *security.SecurityContext {
		t.Helper()
		raw := fmt.Sprintf(`{"TenantId":"t1","UserId":"u1","Roles":[%q],"AccountIds":["acctA","acctB"],"ScopedEntityIds":{%q:%s}}`,
			role, role, mustJSON(t, scoped))
		var sc security.SecurityContext
		if err := json.Unmarshal([]byte(raw), &sc); err != nil {
			t.Fatalf("security context: %v", err)
		}
		return &sc
	}
	cases := []struct {
		name    string
		sc      *security.SecurityContext
		access  security.SecurityAccessType
		account string
		want    bool
	}{
		{"account admin, own account", ctxFor(security.AUTH_ACCOUNT_ADMIN_ROLE, []string{"acctA"}), security.SecurityAccessTypeCreate, "acctA", true},
		{"account admin, another account", ctxFor(security.AUTH_ACCOUNT_ADMIN_ROLE, []string{"acctA"}), security.SecurityAccessTypeCreate, "acctB", false},
		{"account admin, outside any account", ctxFor(security.AUTH_ACCOUNT_ADMIN_ROLE, []string{"acctA"}), security.SecurityAccessTypeCreate, "", false},
		{"account admin, delete in another account", ctxFor(security.AUTH_ACCOUNT_ADMIN_ROLE, []string{"acctA"}), security.SecurityAccessTypeDelete, "acctB", false},
		{"read-only account admin, own account", ctxFor(security.AUTH_ACCOUNT_READ_ADMIN_ROLE, []string{"acctA"}), security.SecurityAccessTypeUpdate, "acctA", false},
		{"tenant admin, any account", ctxFor(security.AUTH_TENANT_ADMIN_ROLE, nil), security.SecurityAccessTypeCreate, "acctB", true},
		{"tenant admin, outside any account", ctxFor(security.AUTH_TENANT_ADMIN_ROLE, nil), security.SecurityAccessTypeCreate, "", true},
		{"tenant admin, account not in the tenant", ctxFor(security.AUTH_TENANT_ADMIN_ROLE, nil), security.SecurityAccessTypeCreate, "acctZ", false},
	}
	for _, tc := range cases {
		if got := manualNodeAuthorizer(tc.sc, tc.access)(tc.account); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
