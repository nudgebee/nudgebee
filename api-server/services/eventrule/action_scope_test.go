package eventrule

import (
	"errors"
	"strings"
	"testing"

	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tenantA      = "aaaaaaaa-0000-0000-0000-000000000000"
	tenantB      = "bbbbbbbb-0000-0000-0000-000000000000"
	acctA1       = "a1a1a1a1-0000-0000-0000-000000000000" // tenant A, the rule's account
	acctA2       = "a2a2a2a2-0000-0000-0000-000000000000" // tenant A, another account
	acctB1       = "b1b1b1b1-0000-0000-0000-000000000000" // tenant B
	integA       = "1a1a1a1a-0000-0000-0000-000000000000" // tenant A, linked to acctA2
	integAUnlink = "1a2a2a2a-0000-0000-0000-000000000000" // tenant A, no linked account
	integB       = "1b1b1b1b-0000-0000-0000-000000000000" // tenant B, linked to acctB1
)

var (
	accountTenant = map[string]string{acctA1: tenantA, acctA2: tenantA, acctB1: tenantB}
	integrations  = map[string][2]string{integA: {tenantA, acctA2}, integAUnlink: {tenantA, ""}, integB: {tenantB, acctB1}}
)

// fakeLookups replaces the database lookups with the fixture above.
func fakeLookups(t *testing.T) {
	t.Helper()
	origShare, origInteg, origTA := accountsShareTenant, integrationInAccountTenant, integrationTenantAndAccount
	t.Cleanup(func() {
		accountsShareTenant, integrationInAccountTenant, integrationTenantAndAccount = origShare, origInteg, origTA
	})
	accountsShareTenant = func(e, o string) (bool, error) {
		te, ok := accountTenant[e]
		return ok && accountTenant[o] == te, nil
	}
	integrationInAccountTenant = func(e, i string) (bool, error) {
		te, ok := accountTenant[e]
		in, found := integrations[i]
		return ok && found && in[0] == te, nil
	}
	integrationTenantAndAccount = func(i string) (string, string, bool, error) {
		in, found := integrations[i]
		return in[0], in[1], found, nil
	}
}

func TestCheckActionScope(t *testing.T) {
	fakeLookups(t)
	cases := []struct {
		name    string
		params  map[string]any
		allowed bool
	}{
		{"no account", map[string]any{"command": "get pods"}, true},
		{"blank account", map[string]any{"account_id": " "}, true},
		{"event's own account", map[string]any{"account_id": acctA1}, true},
		{"own account, different case", map[string]any{"account_id": strings.ToUpper(acctA1)}, true},
		{"another account in the tenant", map[string]any{"account_id": acctA2}, true},
		{"another tenant's account", map[string]any{"account_id": acctB1}, false},
		{"unknown account", map[string]any{"account_id": "cccccccc-0000-0000-0000-000000000000"}, false},
		{"not a uuid", map[string]any{"account_id": "anything"}, false},
		{"not a string", map[string]any{"account_id": 42}, false},
		{"tenant's integration", map[string]any{"datasource_id": integA}, true},
		{"another tenant's integration", map[string]any{"datasource_id": integB}, false},
		{"another tenant's integration with own account", map[string]any{"account_id": acctA1, "datasource_id": integB}, false},
		{"datasource key", map[string]any{"datasource_id": "postgres-main"}, true},
		// Actions decode params case-insensitively (mapstructure), so these set
		// AccountId / DatasourceID too and must be checked the same way.
		{"another tenant's account, mixed-case key", map[string]any{"Account_ID": acctB1}, false},
		{"another tenant's account, upper-case key", map[string]any{"ACCOUNT_ID": acctB1}, false},
		{"own account plus another tenant's under a variant key", map[string]any{"account_id": acctA1, "Account_Id": acctB1}, false},
		{"another tenant's integration, variant key", map[string]any{"DataSource_ID": integB}, false},
		{"another account in the tenant, variant key", map[string]any{"Account_Id": acctA2}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkActionScope(acctA1, tc.params)
			if tc.allowed {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestCheckActionScope_LookupErrorFailsClosed(t *testing.T) {
	fakeLookups(t)
	accountsShareTenant = func(string, string) (bool, error) { return false, errors.New("db down") }
	assert.Error(t, checkActionScope(acctA1, map[string]any{"account_id": acctA2}))
}

// fakeUser can use the accounts it lists, in one tenant.
type fakeUser struct {
	tenant   string
	accounts map[string]bool
}

// Exact match, like SecurityContext.CanAccessAccount (slices.Contains).
func (u fakeUser) CanAccessAccount(id string, _ security.SecurityAccessType) bool {
	return u.accounts[id]
}
func (u fakeUser) GetTenantId() string { return u.tenant }
func (u fakeUser) IsSuperAdmin() bool  { return false }

func TestCheckActionParamAccess(t *testing.T) {
	fakeLookups(t)
	onlyA1 := fakeUser{tenant: tenantA, accounts: map[string]bool{acctA1: true}}
	bothA := fakeUser{tenant: tenantA, accounts: map[string]bool{acctA1: true, acctA2: true}}
	step := func(params map[string]any) []map[string]any {
		return []map[string]any{{"k8s_kubectl": params}}
	}
	cases := []struct {
		name    string
		user    fakeUser
		params  []map[string]any
		allowed bool
	}{
		{"no actions", onlyA1, nil, true},
		{"no account param", onlyA1, step(map[string]any{"command": "get pods"}), true},
		{"rule's own account", onlyA1, step(map[string]any{"account_id": acctA1}), true},
		{"account the user cannot access", onlyA1, step(map[string]any{"account_id": acctA2}), false},
		{"account the user can access", bothA, step(map[string]any{"account_id": acctA2}), true},
		{"accessible account written upper-case", bothA, step(map[string]any{"account_id": strings.ToUpper(acctA2)}), true},
		{"another tenant's account", bothA, step(map[string]any{"account_id": acctB1}), false},
		{"templated account", bothA, step(map[string]any{"account_id": "{{ labels.acct }}"}), false},
		{"templated datasource", bothA, step(map[string]any{"datasource_id": "{% if 1 %}x{% endif %}"}), false},
		{"non-string account", bothA, step(map[string]any{"account_id": 7}), false},
		{"integration on an account the user cannot access", onlyA1, step(map[string]any{"datasource_id": integA}), false},
		{"integration on an account the user can access", bothA, step(map[string]any{"datasource_id": integA}), true},
		{"tenant integration with no account", onlyA1, step(map[string]any{"datasource_id": integAUnlink}), true},
		{"another tenant's integration", bothA, step(map[string]any{"datasource_id": integB}), false},
		{"unknown integration", bothA, step(map[string]any{"datasource_id": "dddddddd-0000-0000-0000-000000000000"}), false},
		{"datasource key", onlyA1, step(map[string]any{"datasource_id": "postgres-main"}), true},
		{"inaccessible account under a variant key", onlyA1, step(map[string]any{"Account_ID": acctA2}), false},
		{"inaccessible integration under a variant key", onlyA1, step(map[string]any{"DATASOURCE_ID": integA}), false},
		{"templated account under a variant key", bothA, step(map[string]any{"ACCOUNT_ID": "{{ labels.acct }}"}), false},
		{"bad step in a later action", onlyA1, []map[string]any{
			{"k8s_kubectl": map[string]any{"account_id": acctA1}},
			{"proxy_ssh_command": map[string]any{"account_id": acctA2}},
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkActionParamAccess(tc.user, acctA1, security.SecurityAccessTypeCreate, tc.params)
			if tc.allowed {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
