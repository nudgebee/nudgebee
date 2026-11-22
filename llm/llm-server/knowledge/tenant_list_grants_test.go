package knowledge

import (
	"database/sql/driver"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
)

// kbPqArrayArg matches a pq.Array(ids) argument.
type kbPqArrayArg []string

func (a kbPqArrayArg) Match(v driver.Value) bool {
	want, err := pq.Array([]string(a)).Value()
	return err == nil && v == want
}

// tenantListCase: readable nil means the caller sees every account (no account
// filter); an empty readable means the call returns nothing without a query.
type tenantListCase struct {
	caller   string
	readable kbPqArrayArg
}

// tenantListCallers adds, for module, the grant holders the tenant-wide lists
// are checked against to the built-in callers.
func tenantListCallers(t *testing.T, module string) map[string]*security.RequestContext {
	return map[string]*security.RequestContext{
		"tenant_admin":          kbGrantCaller(t, []string{security.AUTH_TENANT_ADMIN_ROLE}, nil, nil, nil),
		"tenant_admin_readonly": kbGrantCaller(t, []string{security.AUTH_TENANT_READ_ADMIN_ROLE}, nil, nil, nil),
		"account_admin_a": kbGrantCaller(t, []string{security.AUTH_ACCOUNT_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_ADMIN_ROLE: {orgTestAccount}}, nil, nil),
		"account_admin_readonly_b": kbGrantCaller(t, []string{security.AUTH_ACCOUNT_READ_ADMIN_ROLE},
			map[string][]string{security.AUTH_ACCOUNT_READ_ADMIN_ROLE: {kbGrantAccountB}}, nil, nil),
		"no_role":          kbGrantCaller(t, nil, nil, nil, nil),
		"read":             kbGrantCaller(t, nil, nil, map[string]bool{module + ":Read": true}, nil),
		"write":            kbGrantCaller(t, nil, nil, map[string]bool{module + ":Write": true}, nil),
		"execute":          kbGrantCaller(t, nil, nil, map[string]bool{module + ":Execute": true}, nil),
		"agents_read":      kbGrantCaller(t, nil, nil, map[string]bool{"ai_agents:Read": true}, nil),
		"read_scoped_a":    kbGrantCaller(t, nil, nil, nil, map[string]map[string]bool{orgTestAccount: {module + ":Read": true}}),
		"write_scoped_a":   kbGrantCaller(t, nil, nil, nil, map[string]map[string]bool{orgTestAccount: {module + ":Write": true}}),
		"execute_scoped_a": kbGrantCaller(t, nil, nil, nil, map[string]map[string]bool{orgTestAccount: {module + ":Execute": true}}),
	}
}

var tenantListCases = []tenantListCase{
	{"tenant_admin", nil},
	{"tenant_admin_readonly", nil},
	{"read", nil},
	{"write", nil},
	// An account admin of one account sees that account only, not every
	// account of the tenant.
	{"account_admin_a", kbPqArrayArg{orgTestAccount}},
	{"account_admin_readonly_b", kbPqArrayArg{kbGrantAccountB}},
	{"read_scoped_a", kbPqArrayArg{orgTestAccount}},
	{"write_scoped_a", kbPqArrayArg{orgTestAccount}},
	{"execute", kbPqArrayArg{}},
	{"execute_scoped_a", kbPqArrayArg{}},
	{"agents_read", kbPqArrayArg{}},
	{"no_role", kbPqArrayArg{}},
}

// ai_list_kb without an account: tenant-wide ai_kbs readers keep the
// unfiltered query; anyone else gets the KBs of the accounts they may read.
func TestListKnowledgebasesForTenantGrants(t *testing.T) {
	callers := tenantListCallers(t, "ai_kbs")
	for _, tc := range tenantListCases {
		t.Run(tc.caller, func(t *testing.T) {
			mock := kbGrantMockDB(t)
			if len(tc.readable) > 0 || tc.readable == nil {
				q := mock.ExpectQuery(`FROM llm_knowledgebases kb`)
				if tc.readable == nil {
					q.WithArgs(orgTestTenant)
				} else {
					q.WithArgs(orgTestTenant, tc.readable)
				}
				q.WillReturnRows(sqlmock.NewRows([]string{"id"}))
			}
			kbs, err := ListKnowledgebasesForTenant(callers[tc.caller])
			require.NoError(t, err)
			assert.Empty(t, kbs)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// ai_list_gc without an account: the same scoping on ai_guardrails.
func TestListGlobalContextsForTenantGrants(t *testing.T) {
	callers := tenantListCallers(t, "ai_guardrails")
	for _, tc := range tenantListCases {
		t.Run(tc.caller, func(t *testing.T) {
			mock := kbGrantMockDB(t)
			if len(tc.readable) > 0 || tc.readable == nil {
				q := mock.ExpectQuery(`FROM llm_global_contexts gc`)
				if tc.readable == nil {
					q.WithArgs(orgTestTenant)
				} else {
					q.WithArgs(orgTestTenant, tc.readable)
				}
				q.WillReturnRows(sqlmock.NewRows([]string{"id"}))
			}
			gcs, err := ListGlobalContextsForTenant(callers[tc.caller])
			require.NoError(t, err)
			assert.Empty(t, gcs)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
