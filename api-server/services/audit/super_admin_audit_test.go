package audit

import (
	"context"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The audit writer skips only the synthetic server identity: a human
// super admin is audited; the server's own calls still are not.
func auditMock(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
	})
	t.Cleanup(func() { _ = db.Close() })
	return mock
}

func humanSuperAdmin(t *testing.T) *security.RequestContext {
	t.Helper()
	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson([]byte(
		`{"TenantId":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","UserId":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","Roles":["super_admin"]}`), &sc))
	require.True(t, sc.IsSuperAdmin())
	require.False(t, sc.IsServerInternal())
	return security.NewRequestContext(context.Background(), &sc, nil, nil, nil)
}

func oneAudit() *AuditRequest {
	return &AuditRequest{Audits: []Audit{{
		UserId:        "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		TenantId:      "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		EventTime:     time.Now().UTC(),
		EventCategory: EventCategoryRole,
		EventType:     EventTypeRoleUserCreate,
		EventTarget:   "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		EventState:    map[string]any{"role": "tenant_admin"},
		EventActor:    EventActorApiService,
		EventAction:   EventActionCreate,
		EventStatus:   EventStatusSuccess,
		EventAttr:     map[string]any{"table": "user_roles"},
	}}}
}

func TestCreateAuditRecordsHumanSuperAdmin(t *testing.T) {
	mock := auditMock(t)
	mock.ExpectExec(`INSERT INTO audit`).WillReturnResult(sqlmock.NewResult(1, 1))

	require.NoError(t, CreateAudit(humanSuperAdmin(t), oneAudit()))
	assert.NoError(t, mock.ExpectationsWereMet(), "a human super admin's action is written")
}

func TestCreateAuditSkipsTheServerIdentity(t *testing.T) {
	mock := auditMock(t)
	// No ExpectExec: the server identity must not reach the INSERT.
	require.NoError(t, CreateAudit(security.NewRequestContextForSuperAdmin(nil, nil, nil), oneAudit()))
	assert.NoError(t, mock.ExpectationsWereMet(), "the synthetic server identity is still skipped")
}
