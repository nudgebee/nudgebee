package event

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The event writes and reads below are grantable at the gateway, but their
// handlers used to accept only a built-in role, so a custom-role user the
// gateway admitted was refused here. They now take the action's grant key.

const (
	grantEventTenant  = "5a1e9c37-4b82-4d6f-9a03-7c2d8e1f6b45"
	grantEventAccount = "c63f0a85-2d17-4e9b-8b54-9a1e7d3c0f26"
	grantEventID      = "b7d2e4a1-8c36-4f59-a1d7-3e9c5b0f2a84"
)

// errGrantTestFiltered stands for a read that dropped the caller's row.
var errGrantTestFiltered = errors.New("row filtered out")

func grantEventContext(t *testing.T, wire map[string]any, key string) *security.RequestContext {
	t.Helper()
	raw, err := common.MarshalJson(wire)
	require.NoError(t, err)
	var sc security.SecurityContext
	require.NoError(t, common.UnmarshalJson(raw, &sc))
	ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)
	ctx.SetActionPermission(key)
	return ctx
}

func expectGrantEventLoad(mock sqlmock.Sqlmock, status string) {
	cols := []string{"id", "title", "tenant", "cloud_account_id", "status", "urgency", "finding_id", "description", "source",
		"aggregation_key", "priority", "service_key", "fingerprint", "finding_type", "cluster", "evidences"}
	mock.ExpectQuery("FROM events WHERE id = \\$1").
		WillReturnRows(sqlmock.NewRows(cols).AddRow(grantEventID, "t", grantEventTenant, grantEventAccount, status, "high", "f1", "d", "s",
			"KubePodCrashLooping", "HIGH", "sk", "fp", "issue", "c1", []byte(`[]`)))
}

func TestEventActionsHonourTheActionsGrant(t *testing.T) {
	type call func(t *testing.T, mock sqlmock.Sqlmock, ctx *security.RequestContext) error
	cases := []struct {
		action  string
		key     string
		builtin string // the built-in role that passed before and still must
		call    call
	}{
		{"events_update_resolution", "events:Write", "account_admin", func(_ *testing.T, _ sqlmock.Sqlmock, ctx *security.RequestContext) error {
			// Let through, it stops at the first account read, which the mock does not expect.
			_, err := ApplyEventResolution(ctx, EventRecommendationApplyRequest{AccountId: grantEventAccount, EventId: grantEventID, Data: map[string]any{}})
			return err
		}},
		{"event_update", "events:Write", "account_admin", func(_ *testing.T, mock sqlmock.Sqlmock, ctx *security.RequestContext) error {
			expectGrantEventLoad(mock, "FIRING")
			_, err := UpdateEvent(ctx, models.UpdateEventRequest{EventId: grantEventID, Urgency: "low"})
			return err
		}},
		{"event_update_status", "events:Write", "account_admin", func(_ *testing.T, mock sqlmock.Sqlmock, ctx *security.RequestContext) error {
			// Already CLOSED: a call let through answers without writing.
			expectGrantEventLoad(mock, string(EventStatusClosed))
			_, err := UpdateEventStatus(ctx, UpdateEventStatusRequest{EventID: grantEventID, Status: string(EventStatusClosed), ReasonCode: "false_alarm"}, "user-1")
			return err
		}},
		{"event_get_status_change", "events:Read", "account_admin_readonly", func(t *testing.T, mock sqlmock.Sqlmock, ctx *security.RequestContext) error {
			mock.ExpectQuery("FROM event_history").WillReturnRows(sqlmock.NewRows(
				[]string{"event_id", "cloud_account_id", "changed_by", "new_value", "change_reason", "changed_at", "metadata", "changed_by_name"}).
				AddRow(grantEventID, grantEventAccount, "user-1", []byte(`"CLOSED"`), "manual_close", time.Now(), []byte(`{}`), "Ann"))
			out, err := GetEventManualStatusChanges(ctx, []string{grantEventID})
			require.NoError(t, err)
			if len(out) == 0 {
				return errGrantTestFiltered
			}
			return nil
		}},
	}

	denied := func(err error) bool {
		if errors.Is(err, errGrantTestFiltered) {
			return true
		}
		var e common.Error
		return errors.As(err, &e) && e.Code == 403
	}
	person := func(extra map[string]any) map[string]any {
		wire := map[string]any{"TenantId": grantEventTenant, "UserId": "user-1", "AccountIds": []string{grantEventAccount}, "Roles": []string{}}
		for k, v := range extra {
			wire[k] = v
		}
		return wire
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			run := func(wire map[string]any, key string) error {
				db, mock, err := sqlmock.New()
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
					return &database.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}, nil
				})
				t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })
				return tc.call(t, mock, grantEventContext(t, wire, key))
			}
			grant := map[string]bool{tc.key: true}

			assert.False(t, denied(run(person(map[string]any{"CustomPermissions": grant}), tc.key)), "tenant-wide grant for the key")
			assert.False(t, denied(run(person(map[string]any{"ScopedCustomPermissions": map[string]any{grantEventAccount: grant}}), tc.key)), "grant bound to the account")
			assert.True(t, denied(run(person(nil), tc.key)), "no grant")
			assert.True(t, denied(run(person(map[string]any{"CustomPermissions": map[string]bool{"cost:Write": true}}), tc.key)), "another module's grant")
			assert.True(t, denied(run(person(map[string]any{"CustomPermissions": grant}), "")), "no key: built-in roles only")
			assert.False(t, denied(run(person(map[string]any{
				"Roles": []string{tc.builtin}, "ScopedEntityIds": map[string]any{tc.builtin: []string{grantEventAccount}},
			}), tc.key)), "built-in role")
		})
	}
}
