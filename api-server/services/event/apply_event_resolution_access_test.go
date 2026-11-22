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

// ApplyEventResolution authorizes the account_id the request names, but every
// change it then dispatches — rightsizing, restart, cordon/drain, image
// replace — targets the account of the event it loads by id. Nothing ties the
// two together, so the event's own account has to be authorized as well:
// otherwise a caller names an account they administer next to an event id from
// one they do not, and the change lands on the other account's cluster.

const (
	applyTestEventID       = "b7d2e4a1-8c36-4f59-a1d7-3e9c5b0f2a84"
	applyTestTenant        = "5a1e9c37-4b82-4d6f-9a03-7c2d8e1f6b45"
	applyTestEventAccount  = "c63f0a85-2d17-4e9b-8b54-9a1e7d3c0f26"
	applyTestCallerAccount = "19e8b4d6-7a03-4c52-b6f1-0d5c2a9e8b73"
	applyTestOtherTenant   = "f20c7b59-6e41-4a38-8d95-1b3f7e0a4c68"
)

func setupApplyResolutionTest(t *testing.T) sqlmock.Sqlmock {
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

func applyResolutionTestContext(t *testing.T, wire string) *security.RequestContext {
	t.Helper()
	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson([]byte(wire), &sc))
	return security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
}

// expectApplyResolutionLoads is the two reads every call makes before it knows
// whose event it holds: the account the request names, then the event by id —
// which belongs to applyTestEventAccount whoever asks for it. The event carries
// no aggregation key, so a call that is let through stops at that validation
// instead of dispatching a change.
func expectApplyResolutionLoads(mock sqlmock.Sqlmock, requestedAccount string) {
	mock.ExpectQuery("FROM cloud_accounts WHERE id = \\$1").
		WithArgs(requestedAccount).
		WillReturnRows(sqlmock.NewRows([]string{"id", "account_type"}).AddRow(requestedAccount, "kubernetes"))
	mock.ExpectQuery("FROM events WHERE id = \\$1").
		WithArgs(applyTestEventID, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "title", "tenant", "cloud_account_id"}).
			AddRow(applyTestEventID, "Node not ready", applyTestTenant, applyTestEventAccount))
}

// expectApplyResolutionProceeds is the first thing a call does once the event
// is accepted: read the resolutions already recorded against it.
func expectApplyResolutionProceeds(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("FROM event_resolution WHERE event_id = \\$1").
		WithArgs(applyTestEventID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
}

func TestApplyEventResolution_RefusesEventFromAccountCallerCannotWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire string
	}{
		{
			name: "tenant admin of another tenant",
			wire: `{"TenantId":"` + applyTestOtherTenant + `","UserId":"u1","AccountIds":["` + applyTestCallerAccount + `"],"Roles":["tenant_admin"]}`,
		},
		{
			name: "account admin of a sibling account in the same tenant",
			wire: `{"TenantId":"` + applyTestTenant + `","UserId":"u1","AccountIds":["` + applyTestEventAccount + `","` + applyTestCallerAccount + `"],` +
				`"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["` + applyTestCallerAccount + `"]}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := setupApplyResolutionTest(t)
			expectApplyResolutionLoads(mock, applyTestCallerAccount)
			expectApplyResolutionProceeds(mock)

			// The caller names their own account, which they may write, next to
			// an event that lives in an account they may not.
			_, err := ApplyEventResolution(applyResolutionTestContext(t, tc.wire), EventRecommendationApplyRequest{
				AccountId: applyTestCallerAccount,
				EventId:   applyTestEventID,
				Data:      map[string]any{"cordon": true},
			})

			require.Error(t, err)
			assert.Equal(t, common.ErrorUnauthorized("error: account access not found"), err)

			pending := mock.ExpectationsWereMet()
			require.Error(t, pending, "a refused resolution still went on to act on the event")
			assert.Contains(t, pending.Error(), "event_resolution")
		})
	}
}

func TestApplyEventResolution_AcceptsEventFromAccountCallerCanWrite(t *testing.T) {
	mock := setupApplyResolutionTest(t)
	expectApplyResolutionLoads(mock, applyTestEventAccount)
	expectApplyResolutionProceeds(mock)

	wire := `{"TenantId":"` + applyTestTenant + `","UserId":"u1","AccountIds":["` + applyTestEventAccount + `","` + applyTestCallerAccount + `"],` +
		`"Roles":["account_admin"],"ScopedEntityIds":{"account_admin":["` + applyTestEventAccount + `"]}}`
	_, err := ApplyEventResolution(applyResolutionTestContext(t, wire), EventRecommendationApplyRequest{
		AccountId: applyTestEventAccount,
		EventId:   applyTestEventID,
		Data:      map[string]any{"cordon": true},
	})

	// Past the access check and on to the next validation, not refused.
	require.EqualError(t, err, "recommendation: event has no aggregation key")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A caller who may write both accounts still must name the event's own account.
// The request's account is the one the workload fallback resolves against, while
// the change is dispatched to the event's account: with a mismatch, one account's
// workload record would drive a change on the other account's cluster.
func TestApplyEventResolution_RefusesRequestAccountThatIsNotTheEventsAccount(t *testing.T) {
	mock := setupApplyResolutionTest(t)
	expectApplyResolutionLoads(mock, applyTestCallerAccount)
	expectApplyResolutionProceeds(mock)

	wire := `{"TenantId":"` + applyTestTenant + `","UserId":"u1","AccountIds":["` + applyTestEventAccount + `","` + applyTestCallerAccount + `"],"Roles":["tenant_admin"]}`
	_, err := ApplyEventResolution(applyResolutionTestContext(t, wire), EventRecommendationApplyRequest{
		AccountId: applyTestCallerAccount,
		EventId:   applyTestEventID,
		Data:      map[string]any{"cordon": true},
	})

	require.Error(t, err)
	assert.Equal(t, common.ErrorUnauthorized("error: account access not found"), err)

	pending := mock.ExpectationsWereMet()
	require.Error(t, pending, "a mismatched request still went on to act on the event")
	assert.Contains(t, pending.Error(), "event_resolution")
}

// An undo names a previous attempt by id. It has to be an attempt on this event:
// looked up by id alone, another event's (or another tenant's) recorded workload
// and before-state would be replayed as a change on this one.
func TestBuildUndoRequest_AttemptOfAnotherEventIsNotFound(t *testing.T) {
	mock := setupApplyResolutionTest(t)
	const foreignResolution = "0c4d8e21-5a7b-4f39-9e16-2b8d7c3a5f40"
	mock.ExpectQuery("FROM event_resolution WHERE id = \\$1 AND event_id = \\$2").
		WithArgs(foreignResolution, applyTestEventID).
		WillReturnRows(sqlmock.NewRows([]string{"data", "type_reference_id"}))

	_, _, err := buildUndoRequest(applyResolutionTestContext(t, `{"TenantId":"`+applyTestTenant+`","Roles":["tenant_admin"]}`), applyTestEventID, foreignResolution)

	require.Error(t, err)
	assert.Equal(t, common.ErrorBadRequest("undo: the attempt to undo could not be found"), err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
