package integrations

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"

	"nudgebee/runbook/common"
	"nudgebee/runbook/services/security"
)

// sharedMock is created once per test binary because common.GetDatabaseManager
// caches the *DatabaseManager globally — once set, our hook is never called
// again, so we can't swap in a fresh mock per test.
var sharedMock sqlmock.Sqlmock

func TestMain(m *testing.M) {
	mockDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		panic(fmt.Sprintf("sqlmock setup failed: %v", err))
	}
	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlxDB}, nil
	})
	sharedMock = mock

	code := m.Run()
	_ = mockDB.Close()
	os.Exit(code)
}

func resetTestState(t *testing.T) {
	t.Helper()
	if err := sharedMock.ExpectationsWereMet(); err != nil {
		t.Fatalf("previous test left unmet sqlmock expectations: %v", err)
	}
}

// tenantContext builds a RequestContext with a stable tenantId. We pass a
// non-empty accountIds slice so NewSecurityContextForTenantAccountAdmin's
// internal DB lookup is skipped — otherwise it would issue an unrelated query
// that pollutes our sqlmock expectations.
func tenantContext(tenantId string) *security.RequestContext {
	return security.NewRequestContextForTenantAccountAdmin(tenantId, "test-user", []string{"placeholder"})
}

const (
	githubQueryPattern = `FROM integrations i\s+JOIN integration_config_values icv ON i\.id = icv\.integration_id\s+WHERE i\.tenant_id = \$1\s+AND i\.type = 'github'`
	gitlabQueryPattern = `FROM integrations i\s+JOIN integration_config_values icv ON i\.id = icv\.integration_id\s+WHERE i\.tenant_id = \$1\s+AND i\.type = 'gitlab'`
)

// Validates the structural fix: neither lookup query joins through
// cloud_accounts to derive the tenant. If someone reintroduces that pattern,
// this test fails before any SQL is even executed.
func TestListIntegrationsByType_QueriesUseTenantDirectly(t *testing.T) {
	source, err := os.ReadFile("service.go")
	assert.NoError(t, err)

	whitespace := regexp.MustCompile(`\s+`)
	collapsed := whitespace.ReplaceAllString(string(source), " ")

	assert.NotContains(t, collapsed, "SELECT tenant FROM cloud_accounts WHERE id =",
		"GitHub/GitLab lookups must not derive tenant via cloud_accounts subquery — they should use ctx.GetSecurityContext().GetTenantId() directly")
	assert.Contains(t, collapsed, "WHERE i.tenant_id = $1 AND i.type = 'github'",
		"GitHub lookup should filter by tenant_id directly")
	assert.Contains(t, collapsed, "WHERE i.tenant_id = $1 AND i.type = 'gitlab'",
		"GitLab lookup should filter by tenant_id directly")
}

// The original bug: when the request had no usable accountId, the
// cloud_accounts subquery returned zero rows and the caller saw
// "integration not found". This test reproduces that scenario (empty
// accountId) and asserts the lookup now succeeds because tenant comes from
// the security context.
func TestListIntegrationsByType_GitLab_EmptyAccountIdStillResolvesViaTenant(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-gl-1"

	sharedMock.ExpectQuery(gitlabQueryPattern).
		WithArgs(tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "username", "url", "password"}).
			AddRow("3e862076-bac9-4c85-b927-566490d6b51e", "nudgebee gitlab test e2e", "mangglesh.dagar", "https://gitlab.com", ""))

	ctx := tenantContext(tenantId)
	configs, err := ListIntegrationsByType(ctx, "", "gitlab")

	assert.NoError(t, err)
	// Empty password is filtered out (line 188-190 in service.go), but the
	// query was driven by tenantId and returned a row — proving accountId is
	// no longer load-bearing.
	assert.NotNil(t, configs)
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

// Mirror of the GitLab test for GitHub — same bug, same fix.
func TestListIntegrationsByType_GitHub_EmptyAccountIdStillResolvesViaTenant(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-gh-1"

	sharedMock.ExpectQuery(githubQueryPattern).
		WithArgs(tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "username", "url", "password", "auth_type", "projects"}).
			AddRow("613674b5-9891-4996-be63-74f0eaaeb534", "nudgebee", "nudgebee", "api.github.com", "", "application", `[{"name":"nudgebee","key":"nudgebee/nudgebee"}]`))

	ctx := tenantContext(tenantId)
	configs, err := ListIntegrationsByType(ctx, "", "github")

	assert.NoError(t, err)
	assert.NotNil(t, configs)
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

// AccountId still gets ignored for github/gitlab branches even when present —
// the function signature accepts it for compatibility with non-tenant-scoped
// integration types but the SQL is driven entirely by tenantId.
func TestListIntegrationsByType_GitLab_AccountIdIgnoredWhenPresent(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-gl-2"
	accountId := "any-account-id-the-caller-passed"

	// Expectation explicitly checks tenantId, NOT accountId, is the bound arg.
	sharedMock.ExpectQuery(gitlabQueryPattern).
		WithArgs(tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "username", "url", "password"}))

	ctx := tenantContext(tenantId)
	_, err := ListIntegrationsByType(ctx, accountId, "gitlab")
	assert.NoError(t, err)
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}
