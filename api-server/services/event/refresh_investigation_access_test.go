package event

import (
	"context"
	"log/slog"
	"testing"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RefreshInvestigation loads an event by id alone, then reruns its playbook as
// tenant-admin of the event's own tenant and rewrites its evidence. The caller's
// right to update that event's account is therefore the only thing standing
// between an arbitrary event id and another account's — or another tenant's —
// investigation, exactly as it is for UpdateEvent, UpdateEventStatus and
// AddEvidence.

const (
	refreshTestEventID        = "6f0c1b1e-3d55-4f0a-9c1f-2b7f6a4f8e21"
	refreshTestTenant         = "8b9d3c52-6c1a-4a3e-8f6d-0c5b7a2e9d14"
	refreshTestAccount        = "e2a7c9f4-1b6d-4c83-a5e0-7d3f9b1c6a52"
	refreshTestSiblingAccount = "3c5e8a17-9f42-4d6b-b0a8-1e7c4f2d9b63"
	refreshTestOtherTenant    = "d41f7e96-2a8c-4b05-9e3d-6f1a8c5b2e70"
	refreshTestOtherAccount   = "a90b6d28-5e3f-4c71-8d2a-4b9e7f0c1d35"
)

func setupRefreshTest(t *testing.T) sqlmock.Sqlmock {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	manager := &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	return mock
}

// refreshTestContext builds the caller from the wire shape the gateway session
// produces, so a case reads as the signed-in user it stands for and the caller
// costs no query of its own on the mock.
func refreshTestContext(t *testing.T, wire string) *security.RequestContext {
	t.Helper()
	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson([]byte(wire), &sc))
	return security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
}

// expectRefreshedEventLoad is the unscoped load-by-id: the event belongs to
// refreshTestAccount in refreshTestTenant whoever asks for it.
func expectRefreshedEventLoad(mock sqlmock.Sqlmock, findingType string) {
	mock.ExpectQuery("FROM events WHERE id = \\$1").
		WithArgs(refreshTestEventID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "title", "source", "aggregation_key", "finding_type", "subject_type", "subject_name",
			"evidences", "labels", "tenant", "cloud_account_id",
		}).AddRow(
			refreshTestEventID, "Deployment replicas mismatch", "prometheus", "KubeDeploymentReplicasMismatch",
			findingType, "deployment", "checkout", []byte(`[]`), []byte(`{}`), refreshTestTenant, refreshTestAccount,
		))
}

// expectRefreshRun is what a refresh does to the database once it is let
// through, in order: escalate to tenant-admin of the event's tenant, read the
// account's playbook, rewrite the event's evidence.
func expectRefreshRun(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT id FROM cloud_accounts WHERE tenant = \\$1").
		WithArgs(refreshTestTenant).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(refreshTestAccount).AddRow(refreshTestSiblingAccount))
	mock.ExpectQuery("from agent_playbook").
		WithArgs(refreshTestAccount, "KubeDeploymentReplicasMismatch").
		WillReturnRows(sqlmock.NewRows([]string{"action_params"}))
	mock.ExpectExec("UPDATE events SET evidences").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), refreshTestEventID).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func TestRefreshInvestigation_RefusesCallerWithoutAccountAccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire string
	}{
		{
			name: "tenant admin of another tenant",
			wire: `{"TenantId":"` + refreshTestOtherTenant + `","UserId":"u1","AccountIds":["` + refreshTestOtherAccount + `"],"Roles":["tenant_admin"]}`,
		},
		{
			name: "account admin of a sibling account in the same tenant",
			wire: `{"TenantId":"` + refreshTestTenant + `","UserId":"u1","AccountIds":["` + refreshTestAccount + `","` + refreshTestSiblingAccount + `"],` +
				`"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["` + refreshTestSiblingAccount + `"]}}`,
		},
		{
			name: "read-only tenant admin of the event's own tenant",
			wire: `{"TenantId":"` + refreshTestTenant + `","UserId":"u1","AccountIds":["` + refreshTestAccount + `"],"Roles":["tenant_admin_readonly"]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupRefreshTest(t)
			expectRefreshedEventLoad(mock, "issue")
			expectRefreshRun(mock)

			err := RefreshInvestigation(refreshTestContext(t, tc.wire), refreshTestEventID)

			require.Error(t, err)
			assert.Equal(t, common.ErrorUnauthorized("access denied for account: "+refreshTestAccount), err)

			// Nothing past the load may have run. Expectations are ordered, so the
			// escalation query still pending means the playbook read and the
			// evidence rewrite behind it never happened either.
			pending := mock.ExpectationsWereMet()
			require.Error(t, pending, "a refused refresh still reran the playbook and rewrote the event's evidence")
			assert.Contains(t, pending.Error(), "cloud_accounts")
		})
	}
}

// The event here is a configuration change: the one kind a refresh returns from
// without running a playbook, which keeps these cases off the enrichers and the
// relay. The access check sits ahead of that return, so a caller who reaches it
// has been let through.
func TestRefreshInvestigation_AllowsCallerWithAccountAccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire string
	}{
		{
			// Also what both internal callers run as: every ingest path hands
			// InvestigateEvent's subject-resolved re-run a tenant-admin context
			// for the event's tenant.
			name: "tenant admin of the event's own tenant",
			wire: `{"TenantId":"` + refreshTestTenant + `","AccountIds":["` + refreshTestAccount + `","` + refreshTestSiblingAccount + `"],"Roles":["account_admin","tenant_admin"]}`,
		},
		{
			name: "account admin of the event's account",
			wire: `{"TenantId":"` + refreshTestTenant + `","UserId":"u1","AccountIds":["` + refreshTestAccount + `","` + refreshTestSiblingAccount + `"],` +
				`"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["` + refreshTestAccount + `"]}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupRefreshTest(t)
			expectRefreshedEventLoad(mock, "configuration_change")

			require.NoError(t, RefreshInvestigation(refreshTestContext(t, tc.wire), refreshTestEventID))
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
