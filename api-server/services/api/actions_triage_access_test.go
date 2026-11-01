package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	triageOwnAccount     = "11111111-1111-1111-1111-111111111111" // same tenant, caller may update
	triageSiblingAccount = "22222222-2222-2222-2222-222222222222" // same tenant, account admin is read-only
	triageForeignAccount = "33333333-3333-3333-3333-333333333333" // another tenant's account
	triageEventID        = "44444444-4444-4444-4444-444444444444"
	triageRuleID         = "55555555-5555-5555-5555-555555555555"
	triageResourceID     = "66666666-6666-6666-6666-666666666666"

	// triageEventLoadSQL matches event.GetEvent, the read every event-keyed
	// handler does before it knows which account the event belongs to.
	triageEventLoadSQL = "score_confidence FROM events WHERE id"

	// An account_admin of triageOwnAccount who can only read triageSiblingAccount.
	triageAccountAdmin = `{"TenantId":"tenant-1","UserId":"user-1",
		"AccountIds":["` + triageOwnAccount + `","` + triageSiblingAccount + `"],
		"Roles":["account_admin","account_admin_readonly"],
		"ScopedEntityIds":{"account_admin":["` + triageOwnAccount + `"],"account_admin_readonly":["` + triageSiblingAccount + `"]}}`

	// A user with no built-in role whose custom role grants the two modules
	// tenant-wide. The gateway admits these callers, so the handlers must too.
	triageCustomRoleUser = `{"TenantId":"tenant-1","UserId":"user-2",
		"AccountIds":["` + triageOwnAccount + `","` + triageSiblingAccount + `"],
		"Roles":[],
		"CustomPermissions":{"events:Write":true,"workload:Write":true}}`
)

// triageAccessCase is one /rpc/triage action: the input that targets an account
// (directly, or through an event that lives in it) and the statements the
// handler runs once it has decided to act on that account.
type triageAccessCase struct {
	action     string
	handler    func(*ActionRequest, *gin.Context, *security.RequestContext)
	readOnly   bool // read access to the account is enough
	loadsEvent bool
	input      func(accountID string) map[string]any
	queries    func(mock sqlmock.Sqlmock, accountID string)
}

var triageAccessCases = []triageAccessCase{
	{
		action:     "event_update_nb_status",
		handler:    handleEventUpdateNBStatus,
		loadsEvent: true,
		input: func(string) map[string]any {
			return map[string]any{"event_id": triageEventID, "nb_status": "ACKNOWLEDGED"}
		},
		queries: func(mock sqlmock.Sqlmock, _ string) {
			mock.ExpectQuery("SELECT nb_status FROM events").
				WillReturnRows(sqlmock.NewRows([]string{"nb_status"}).AddRow("OPEN"))
			mock.ExpectExec("UPDATE events SET nb_status").WillReturnResult(sqlmock.NewResult(0, 1))
		},
	},
	{
		action:     "events_update_classification",
		handler:    handleEventClassify,
		loadsEvent: true,
		input: func(string) map[string]any {
			return map[string]any{"event_id": triageEventID, "classification": "true_positive", "reason_code": "correct_severity"}
		},
		queries: func(mock sqlmock.Sqlmock, accountID string) {
			mock.ExpectQuery("FROM events WHERE id = \\$1 AND cloud_account_id = \\$2").
				WillReturnRows(sqlmock.NewRows([]string{"id", "title", "cloud_account_id", "tenant"}).
					AddRow(triageEventID, "pod crash looping", accountID, "tenant-of-the-event"))
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO event_classification").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE events SET nb_status").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			mock.ExpectExec("INSERT INTO event_history").WillReturnResult(sqlmock.NewResult(0, 1))
		},
	},
	{
		// The preview of the action above: writes nothing, but returns the event's
		// title and those of its same-fingerprint siblings.
		action:     "events_dryrun_classification",
		handler:    handleEventClassifyPreview,
		readOnly:   true,
		loadsEvent: true,
		input: func(string) map[string]any {
			return map[string]any{"event_id": triageEventID, "classification": "false_positive"}
		},
		queries: func(mock sqlmock.Sqlmock, accountID string) {
			mock.ExpectQuery("FROM events WHERE id = \\$1 AND cloud_account_id = \\$2").
				WillReturnRows(sqlmock.NewRows([]string{"id", "title", "cloud_account_id", "tenant"}).
					AddRow(triageEventID, "pod crash looping", accountID, "tenant-of-the-event"))
		},
	},
	{
		action:  "event_create_triage_rule",
		handler: handleEventCreateTriageRule,
		input: func(accountID string) map[string]any {
			return map[string]any{
				"cloud_account_id": accountID, "rule_type": "suppression", "action": "suppress",
				"match_fingerprint": "fp-1", "apply_to_existing": true,
			}
		},
		queries: func(mock sqlmock.Sqlmock, _ string) {
			mock.ExpectExec("INSERT INTO event_triage_rules").WillReturnResult(sqlmock.NewResult(0, 1))
			// apply_to_existing rewrites nb_status on every matching open event of the account.
			mock.ExpectQuery("UPDATE events SET nb_status").WillReturnRows(sqlmock.NewRows([]string{"id", "tenant"}))
		},
	},
	{
		action:  "event_update_triage_rule",
		handler: handleEventUpdateTriageRule,
		input: func(accountID string) map[string]any {
			return map[string]any{"cloud_account_id": accountID, "rule_id": triageRuleID, "rule_type": "suppression", "action": "suppress"}
		},
		queries: func(mock sqlmock.Sqlmock, accountID string) {
			mock.ExpectQuery("SELECT id, is_editable, account_id FROM event_triage_rules").
				WillReturnRows(sqlmock.NewRows([]string{"id", "is_editable", "account_id"}).AddRow(triageRuleID, true, accountID))
			mock.ExpectQuery("UPDATE event_triage_rules").
				WillReturnRows(sqlmock.NewRows([]string{"id", "rule_type", "action"}).AddRow(triageRuleID, "suppression", "suppress"))
		},
	},
	{
		action:  "event_delete_triage_rule",
		handler: handleEventDeleteTriageRule,
		input: func(accountID string) map[string]any {
			return map[string]any{"cloud_account_id": accountID, "rule_id": triageRuleID}
		},
		queries: func(mock sqlmock.Sqlmock, _ string) {
			mock.ExpectExec("UPDATE event_triage_rules SET enabled = FALSE").WillReturnResult(sqlmock.NewResult(0, 1))
		},
	},
	{
		action:  "events_update_rule_override",
		handler: handleEventToggleSystemRuleOverride,
		input: func(accountID string) map[string]any {
			return map[string]any{"cloud_account_id": accountID, "system_rule_id": triageRuleID, "disabled": true}
		},
		queries: func(mock sqlmock.Sqlmock, _ string) {
			mock.ExpectQuery("SELECT id, tenant_id, account_id, can_override FROM event_triage_rules").
				WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "account_id", "can_override"}).AddRow(triageRuleID, nil, nil, true))
			mock.ExpectExec("INSERT INTO event_triage_rule_overrides").WillReturnResult(sqlmock.NewResult(0, 1))
		},
	},
	{
		action:  "workload_upsert_criticality",
		handler: handleWorkloadUpsertCriticality,
		input: func(accountID string) map[string]any {
			return map[string]any{"cloud_account_id": accountID, "cloud_resource_id": triageResourceID, "criticality": "high"}
		},
		queries: func(mock sqlmock.Sqlmock, _ string) {
			mock.ExpectExec("INSERT INTO workload_criticality").WillReturnResult(sqlmock.NewResult(0, 1))
		},
	},
	{
		action:  "workload_delete_criticality",
		handler: handleWorkloadDeleteCriticality,
		input: func(accountID string) map[string]any {
			return map[string]any{"cloud_account_id": accountID, "cloud_resource_id": triageResourceID}
		},
		queries: func(mock sqlmock.Sqlmock, _ string) {
			mock.ExpectExec("DELETE FROM workload_criticality").WillReturnResult(sqlmock.NewResult(0, 1))
		},
	},
	{
		action:     "event_get_duplicate_suggestions",
		handler:    handleEventGetDuplicateSuggestions,
		readOnly:   true,
		loadsEvent: true,
		input: func(string) map[string]any {
			return map[string]any{"event_id": triageEventID}
		},
		queries: func(mock sqlmock.Sqlmock, _ string) {
			mock.ExpectQuery("WITH current_event AS").WillReturnRows(sqlmock.NewRows([]string{"id"}))
		},
	},
	{
		action:   "workload_list_criticality",
		handler:  handleWorkloadListCriticality,
		readOnly: true,
		input: func(accountID string) map[string]any {
			return map[string]any{"cloud_account_id": accountID}
		},
		queries: func(mock sqlmock.Sqlmock, _ string) {
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM k8s_workloads").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
			mock.ExpectQuery("AS cloud_resource_id, w.namespace").WillReturnRows(sqlmock.NewRows([]string{"cloud_resource_id"}))
		},
	},
}

// TestTriageActionsRequireAccountAccess covers the /rpc/triage actions that act
// on an account named by the request. The triage package trusts the account it
// is handed, and these handlers used to take it straight from the input — or
// from the event the input named — without asking whether the caller may touch
// it. Any role admitted by actions.yaml could therefore change the triage
// status, classification, rules or workload criticality of an account in
// another tenant, or of a sibling account it can only read.
func TestTriageActionsRequireAccountAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	manager := &database.DatabaseManager{}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return manager, nil
	})
	t.Cleanup(func() { database.RegisterDatabaseManagerHook(database.Metastore, nil) })

	// run calls the handler as caller against an account and returns the HTTP
	// status, the statements that ran after the event load, and the mock.
	run := func(t *testing.T, tc triageAccessCase, caller, accountID string) (int, []string, sqlmock.Sqlmock) {
		t.Helper()
		var (
			mu  sync.Mutex
			ran []string
		)
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherFunc(func(expected, actual string) error {
			if err := sqlmock.QueryMatcherRegexp.Match(expected, actual); err != nil {
				return err
			}
			if expected != triageEventLoadSQL {
				mu.Lock()
				ran = append(ran, actual)
				mu.Unlock()
			}
			return nil
		})))
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		manager.Db = sqlx.NewDb(db, "postgres")

		if tc.loadsEvent {
			mock.ExpectQuery(triageEventLoadSQL).
				WillReturnRows(sqlmock.NewRows([]string{"id", "tenant", "cloud_account_id"}).
					AddRow(triageEventID, "tenant-of-the-event", accountID))
		}
		tc.queries(mock, accountID)

		var sc security.SecurityContext
		require.NoError(t, json.Unmarshal([]byte(caller), &sc))
		ctx := security.NewRequestContext(context.Background(), &sc, slog.Default(), nil, nil)

		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		tc.handler(&ActionRequest{Input: tc.input(accountID)}, c, ctx)

		mu.Lock()
		defer mu.Unlock()
		return rec.Code, append([]string(nil), ran...), mock
	}

	for _, tc := range triageAccessCases {
		scenarios := []struct {
			name      string
			caller    string
			accountID string
			allowed   bool
		}{
			{"account admin on own account", triageAccountAdmin, triageOwnAccount, true},
			{"account admin on a read-only sibling account", triageAccountAdmin, triageSiblingAccount, tc.readOnly},
			{"account admin on another tenant's account", triageAccountAdmin, triageForeignAccount, false},
			{"custom-role user on own tenant's account", triageCustomRoleUser, triageOwnAccount, true},
			{"custom-role user on another tenant's account", triageCustomRoleUser, triageForeignAccount, false},
		}
		for _, s := range scenarios {
			t.Run(tc.action+"/"+s.name, func(t *testing.T) {
				status, ran, mock := run(t, tc, s.caller, s.accountID)

				if s.allowed {
					assert.Equal(t, http.StatusOK, status)
					assert.NoError(t, mock.ExpectationsWereMet())
					return
				}
				assert.Equal(t, http.StatusForbidden, status)
				assert.Empty(t, ran, "handler acted on an account the caller cannot access")
			})
		}
	}
}
