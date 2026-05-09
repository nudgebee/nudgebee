package integrations

import (
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

func TestResolveIntegrationID_UUIDPassthrough(t *testing.T) {
	resetTestState(t)
	ctx := tenantContext("tenant-x")

	const id = "613674b5-9891-4996-be63-74f0eaaeb534"
	got, err := ResolveIntegrationID(ctx, id, []string{"jira"})
	assert.NoError(t, err)
	assert.Equal(t, id, got)
	// Crucially, no sqlmock expectation was registered — UUIDs must skip the DB.
	assert.NoError(t, sharedMock.ExpectationsWereMet())
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
