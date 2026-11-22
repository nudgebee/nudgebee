package api

import (
	"testing"

	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"
)

const (
	gateAcctA   = "11111111-1111-1111-1111-111111111111"
	gateAcctB   = "22222222-2222-2222-2222-222222222222"
	gateForeign = "99999999-9999-9999-9999-999999999999"
)

func gateContext(t *testing.T, wire string) *security.SecurityContext {
	t.Helper()
	var sc security.SecurityContext
	if err := common.UnmarshalJson([]byte(wire), &sc); err != nil {
		t.Fatalf("unmarshal security context: %v", err)
	}
	return &sc
}

// The account gate lets a principal holding only custom roles reach the
// workflow endpoints when a workflows grant covers the account, and nothing
// else changes.
func TestMayReachAccount(t *testing.T) {
	accounts := `"AccountIds":["` + gateAcctA + `","` + gateAcctB + `"]`
	cases := []struct {
		name string
		wire string
		acct string
		want bool
	}{
		{"tenant-wide workflows grant, account in the tenant",
			`{"TenantId":"t1","UserId":"u1","Roles":[],` + accounts + `,"CustomPermissions":{"workflows:Execute":true}}`, gateAcctA, true},
		{"tenant-wide workflows grant, account outside the tenant",
			`{"TenantId":"t1","UserId":"u1","Roles":[],` + accounts + `,"CustomPermissions":{"workflows:Read":true}}`, gateForeign, false},
		{"account-scoped workflows grant, that account",
			`{"TenantId":"t1","UserId":"u1","Roles":[],` + accounts + `,"ScopedCustomPermissions":{"` + gateAcctB + `":{"workflows:Write":true}}}`, gateAcctB, true},
		{"account-scoped workflows grant, another account",
			`{"TenantId":"t1","UserId":"u1","Roles":[],` + accounts + `,"ScopedCustomPermissions":{"` + gateAcctB + `":{"workflows:Write":true}}}`, gateAcctA, false},
		{"custom role with no workflows grant",
			`{"TenantId":"t1","UserId":"u1","Roles":[],` + accounts + `,"CustomPermissions":{"dashboards:Read":true,"llm:Execute":true}}`, gateAcctA, false},
		{"no roles and no grants",
			`{"TenantId":"t1","UserId":"u1","Roles":[],` + accounts + `}`, gateAcctA, false},
		{"built-in tenant admin, unchanged",
			`{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin"],` + accounts + `}`, gateAcctA, true},
		{"built-in tenant admin, account outside the tenant, unchanged",
			`{"TenantId":"t1","UserId":"u1","Roles":["tenant_admin"],` + accounts + `}`, gateForeign, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mayReachAccount(gateContext(t, tc.wire), tc.acct); got != tc.want {
				t.Errorf("mayReachAccount = %v, want %v", got, tc.want)
			}
		})
	}
}
