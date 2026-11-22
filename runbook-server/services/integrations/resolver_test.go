package integrations

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

const resolverQueryPattern = `SELECT id::text FROM integrations\s+WHERE tenant_id = \$1\s+AND LOWER\(name\) = LOWER\(\$2\)\s+AND type = ANY\(\$3\)\s+AND status = 'enabled'`

func TestResolveIntegrationID_EmptyValue(t *testing.T) {
	resetTestState(t)
	ctx := tenantContext("tenant-x")

	got, err := ResolveIntegrationID(ctx, "", []string{"jira"})
	assert.NoError(t, err)
	assert.Equal(t, "", got)
}

const (
	uuidQueryPattern      = `SELECT name, type FROM integrations WHERE id = \$1 AND tenant_id = \$2`
	otherKindQueryPattern = `SELECT type, status FROM integrations WHERE tenant_id = \$1 AND LOWER\(name\) = LOWER\(\$2\)`
	availableQueryPattern = `SELECT name, status FROM integrations WHERE tenant_id = \$1 AND type = ANY\(\$2\) ORDER BY name LIMIT 20`
)

func TestResolveIntegrationID_UUIDOfExpectedKind(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-uuid-1"
	const id = "613674b5-9891-4996-be63-74f0eaaeb534"

	sharedMock.ExpectQuery(uuidQueryPattern).
		WithArgs(id, tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"name", "type"}).AddRow("prod-jira", "jira"))

	got, err := ResolveIntegrationID(tenantContext(tenantId), id, []string{"jira", "github"})
	assert.NoError(t, err)
	assert.Equal(t, id, got)
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

// A webhook integration handed to a ticket step used to pass straight through and fail inside
// the ticket call; it is now refused with the kind it actually is.
func TestResolveIntegrationID_UUIDOfWrongKind(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-uuid-2"
	const id = "11111111-2222-3333-4444-555555555555"

	sharedMock.ExpectQuery(uuidQueryPattern).
		WithArgs(id, tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"name", "type"}).AddRow("incident-webhook", "workflow_webhook"))

	_, err := ResolveIntegrationID(tenantContext(tenantId), id, []string{"servicenow"})
	assert.EqualError(t, err, "integration 'incident-webhook' is a workflow_webhook integration; this step needs a servicenow integration")
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

func TestResolveIntegrationID_UUIDMissing_NoneOfKindConnected(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-uuid-3"
	const id = "22222222-3333-4444-5555-666666666666"

	sharedMock.ExpectQuery(uuidQueryPattern).WithArgs(id, tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"name", "type"}))
	sharedMock.ExpectQuery(otherKindQueryPattern).WithArgs(tenantId, id, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"type", "status"}))
	sharedMock.ExpectQuery(availableQueryPattern).WithArgs(tenantId, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"name", "status"}))

	_, err := ResolveIntegrationID(tenantContext(tenantId), id, []string{"jira", "servicenow", "github"})
	assert.EqualError(t, err, "this account has no jira, servicenow or github integration connected")
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

func TestResolveIntegrationID_UUIDMissing_ListsWhatTheAccountHas(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-uuid-4"
	const id = "33333333-4444-5555-6666-777777777777"

	sharedMock.ExpectQuery(uuidQueryPattern).WithArgs(id, tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"name", "type"}))
	sharedMock.ExpectQuery(otherKindQueryPattern).WithArgs(tenantId, id, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"type", "status"}))
	sharedMock.ExpectQuery(availableQueryPattern).WithArgs(tenantId, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"name", "status"}).AddRow("orders-db", "enabled").AddRow("users-db", "enabled"))

	_, err := ResolveIntegrationID(tenantContext(tenantId), id, []string{"postgresql", "mysql"})
	assert.EqualError(t, err, "integration '"+id+"' not found on this account; postgresql or mysql integrations it has: orders-db, users-db")
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

func TestResolveIntegrationID_UUIDWithoutExpectedTypes(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-uuid-5"
	const id = "44444444-5555-6666-7777-888888888888"

	sharedMock.ExpectQuery(uuidQueryPattern).WithArgs(id, tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"name", "type"}).AddRow("anything", "slack"))
	got, err := ResolveIntegrationID(tenantContext(tenantId), id, nil)
	assert.NoError(t, err)
	assert.Equal(t, id, got)

	sharedMock.ExpectQuery(uuidQueryPattern).WithArgs(id, tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"name", "type"}))
	_, err = ResolveIntegrationID(tenantContext(tenantId), id, nil)
	assert.EqualError(t, err, "integration '"+id+"' not found on this account")
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

func TestResolveIntegrationID_NameOfWrongKind(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-name-kind"

	sharedMock.ExpectQuery(resolverQueryPattern).
		WithArgs(tenantId, "team-mcp", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	sharedMock.ExpectQuery(otherKindQueryPattern).WithArgs(tenantId, "team-mcp", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"type", "status"}).AddRow("mcp", "enabled"))

	_, err := ResolveIntegrationID(tenantContext(tenantId), "team-mcp", []string{"servicenow"})
	assert.EqualError(t, err, "integration 'team-mcp' is a mcp integration; this step needs a servicenow integration")
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

func TestResolveIntegrationID_NameOfDisabledIntegration(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-name-disabled"

	sharedMock.ExpectQuery(resolverQueryPattern).
		WithArgs(tenantId, "prod-jira", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	sharedMock.ExpectQuery(otherKindQueryPattern).WithArgs(tenantId, "prod-jira", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"type", "status"}).AddRow("jira", "disabled"))

	_, err := ResolveIntegrationID(tenantContext(tenantId), "prod-jira", []string{"jira"})
	assert.EqualError(t, err, "integration 'prod-jira' is disabled; enable it under Integrations")
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

func TestResolveIntegrationID_OnlyDisabledOfTheKind(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-only-disabled"

	sharedMock.ExpectQuery(resolverQueryPattern).
		WithArgs(tenantId, "jira", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	sharedMock.ExpectQuery(otherKindQueryPattern).WithArgs(tenantId, "jira", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"type", "status"}))
	sharedMock.ExpectQuery(availableQueryPattern).WithArgs(tenantId, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"name", "status"}).AddRow("old-jira", "disabled"))

	_, err := ResolveIntegrationID(tenantContext(tenantId), "jira", []string{"jira"})
	assert.EqualError(t, err, "integration 'jira' not found on this account; its jira integrations are disabled: old-jira — enable one under Integrations")
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

// A database error while explaining the failure is returned as an error, not as "none connected".
func TestResolveIntegrationID_LookupErrorIsNotReportedAsMissing(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-db-error"
	const id = "55555555-6666-7777-8888-999999999999"

	sharedMock.ExpectQuery(uuidQueryPattern).WithArgs(id, tenantId).
		WillReturnRows(sqlmock.NewRows([]string{"name", "type"}))
	sharedMock.ExpectQuery(otherKindQueryPattern).WithArgs(tenantId, id, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"type", "status"}))
	sharedMock.ExpectQuery(availableQueryPattern).WithArgs(tenantId, sqlmock.AnyArg()).
		WillReturnError(errors.New("connection reset"))

	_, err := ResolveIntegrationID(tenantContext(tenantId), id, []string{"jira"})
	assert.EqualError(t, err, "failed to list jira integrations: connection reset")
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

func TestKindList(t *testing.T) {
	assert.Equal(t, "", kindList(nil))
	assert.Equal(t, "jira", kindList([]string{"jira"}))
	assert.Equal(t, "jira or github", kindList([]string{"jira", "github"}))
	assert.Equal(t, "jira, github or gitlab", kindList([]string{"jira", "github", "gitlab"}))
}

func TestResolveIntegrationID_NameResolves(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-resolve-1"

	sharedMock.ExpectQuery(resolverQueryPattern).
		WithArgs(tenantId, "prod-jira", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).
			AddRow("a1b2c3d4-e5f6-7890-abcd-ef1234567890"))

	ctx := tenantContext(tenantId)
	got, err := ResolveIntegrationID(ctx, "prod-jira", []string{"jira", "github"})

	assert.NoError(t, err)
	assert.Equal(t, "a1b2c3d4-e5f6-7890-abcd-ef1234567890", got)
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

func TestResolveIntegrationID_NameNotFound(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-resolve-2"

	sharedMock.ExpectQuery(resolverQueryPattern).
		WithArgs(tenantId, "missing", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	sharedMock.ExpectQuery(otherKindQueryPattern).WithArgs(tenantId, "missing", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"type", "status"}))
	sharedMock.ExpectQuery(availableQueryPattern).WithArgs(tenantId, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"name", "status"}).AddRow("prod-jira", "enabled"))

	ctx := tenantContext(tenantId)
	_, err := ResolveIntegrationID(ctx, "missing", []string{"jira"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

func TestResolveIntegrationID_NameAmbiguous(t *testing.T) {
	resetTestState(t)
	tenantId := "tenant-resolve-3"

	sharedMock.ExpectQuery(resolverQueryPattern).
		WithArgs(tenantId, "prod", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).
			AddRow("11111111-1111-1111-1111-111111111111").
			AddRow("22222222-2222-2222-2222-222222222222"))

	ctx := tenantContext(tenantId)
	_, err := ResolveIntegrationID(ctx, "prod", []string{"jira"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ambiguous")
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}

func TestResolveIntegrationID_EmptyExpectedTypes(t *testing.T) {
	resetTestState(t)
	ctx := tenantContext("tenant-x")

	_, err := ResolveIntegrationID(ctx, "some-name", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected integration type")
	// No DB expectation should have been registered or consumed.
	assert.NoError(t, sharedMock.ExpectationsWereMet())
}
