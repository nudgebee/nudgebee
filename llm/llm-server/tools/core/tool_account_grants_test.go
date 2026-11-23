package core

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security"
)

type toolAcctCase struct {
	name   string
	caller *security.RequestContext
	allow  bool
}

// toolAccountCases are the callers of an ai_tools action of class on account
// B: built-in roles as before, the ai_tools grant at the class (tenant-wide or
// on B; Write also reads), and the refusals: another module, another class,
// an account-bound grant on another account, no grant.
func toolAccountCases(t *testing.T, class string) []toolAcctCase {
	perm := func(c string) map[string]bool { return map[string]bool{"ai_tools:" + c: true} }
	cases := []toolAcctCase{
		{"tenant_admin", grantCaller(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil, nil), true},
		{"account_admin_b", grantCaller(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {grantAccountB}}, nil, nil), true},
		{"account_admin_readonly_b", grantCaller(t, []string{security.AUTH_ACCOUNT_READ_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_READ_ADMIN_ROLE: {grantAccountB}}, nil, nil), class == "Read"},
		{"account_admin_a", grantCaller(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {grantAccountA}}, nil, nil), false},
		{"no_role", grantCaller(t, nil, nil, nil, nil), false},
		{"ai_tools:" + class, grantCaller(t, nil, nil, perm(class), nil), true},
		{"ai_tools:" + class + " on b", grantCaller(t, nil, nil, nil, map[string]map[string]bool{grantAccountB: perm(class)}), true},
		{"ai_tools:" + class + " on a", grantCaller(t, nil, nil, nil, map[string]map[string]bool{grantAccountA: perm(class)}), false},
		{"ai_agents:" + class, grantCaller(t, nil, nil, map[string]bool{"ai_agents:" + class: true}, nil), false},
		{"ai_tools:Execute", grantCaller(t, nil, nil, perm("Execute"), nil), false},
	}
	if class == "Read" {
		cases = append(cases, toolAcctCase{"ai_tools:Write", grantCaller(t, nil, nil, perm("Write"), nil), true})
	} else {
		cases = append(cases, toolAcctCase{"ai_tools:Read", grantCaller(t, nil, nil, perm("Read"), nil), false})
	}
	return cases
}

// ai_list_tools for one account: a built-in reader or an ai_tools Read/Write
// grant there gets the account's tool list (seeded in the cache); anyone else
// an empty one.
func TestListToolsAccountGrants(t *testing.T) {
	toolDtoCacheInstance.set(grantAccountB, []ToolDto{{Name: "deploy_check"}})
	t.Cleanup(func() { toolDtoCacheInstance.delete(grantAccountB) })
	for _, tc := range toolAccountCases(t, "Read") {
		t.Run(tc.name, func(t *testing.T) {
			grantMockDB(t)
			tools := ListTools(tc.caller, grantAccountB)
			listed := len(tools) > 0 && tools[0].Name == "deploy_check"
			assert.Equal(t, tc.allow, listed, "got %v", tools)
		})
	}
}

// ai_create_tool, ai_update_tool and ai_delete_tool on an account tool: a
// built-in role that writes the account, or an ai_tools:Write grant there.
func TestCustomToolAccountWriteGrants(t *testing.T) {
	actions := []struct {
		action  string
		account bool // the call locks the tool's owner (an account tool) first
		call    func(*security.RequestContext) error
	}{
		{"ai_create_tool", false, func(sc *security.RequestContext) error {
			_, err := CreateCustomTool(sc, grantAccountB, ToolDto{Name: "deploy_check", Description: "checks a deploy"})
			return err
		}},
		{"ai_update_tool", true, func(sc *security.RequestContext) error {
			return UpdateCustomTool(sc, grantAccountB, "deploy_check", ToolDto{Id: grantItem, Name: "deploy_check", Status: ToolStatusEnabled})
		}},
		{"ai_delete_tool", false, func(sc *security.RequestContext) error {
			return DeleteCustomTool(sc, grantAccountB, "deploy_check")
		}},
	}
	for _, a := range actions {
		t.Run(a.action, func(t *testing.T) {
			for _, tc := range toolAccountCases(t, "Write") {
				t.Run(tc.name, func(t *testing.T) {
					mock := grantMockDB(t)
					if a.account {
						expectLockedOwner(mock, grantTenant, VisibilityAccount)
					}
					assertOrgGrant(t, a.call(tc.caller), tc.allow, errUnauthorized)
				})
			}
		})
	}
}
