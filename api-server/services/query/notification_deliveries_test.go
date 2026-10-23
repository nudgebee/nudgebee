package query

import (
	"fmt"
	"os"
	"sort"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func deliveriesRequest(table string) QueryRequest {
	return QueryRequest{Table: table, Columns: fromStringColsToQueryCols([]string{"id"}), Limit: 10}
}

func TestNotificationDeliveries_ActionsReadScopedTablesWithoutState(t *testing.T) {
	for action, table := range map[string]string{
		"notifications_list_deliveries":      "notification_deliveries_v2",
		"notifications_aggregate_deliveries": "notification_deliveries_grouping_v2",
	} {
		def, ok := GetTableMetadata(action)
		require.True(t, ok, action)
		assert.Equal(t, table, def.Name)
		assert.Equal(t, "tenant_id", def.TenantIdColumnName)
		assert.Equal(t, "account_id", def.AccountIdColumnName)
		assert.Equal(t, "events", def.PermissionModule, "visible like alerts")
		assert.False(t, def.TenantWideReadable, "rows with no account are not for account readers")
		for _, state := range []string{"slack_metadata", "teams_metadata", "slack_thread_id", "slack_team_id",
			"slack_channel_id", "teams_message_id", "teams_channel_id", "discord_channel_id", "discord_message_id"} {
			assert.NotContains(t, def.Columns, state, "%s must not expose %s", table, state)
		}
		assert.Contains(t, def.Def, "'remediation_marker', 'remediation_pending', 'nubi_answer'")
		assert.Contains(t, def.Def, "sn.kind IS NOT NULL")
	}
}

func TestNotificationDeliveries_AccountAdminSeesTheirAccountsAndNoEmail(t *testing.T) {
	ctx := scopedCtx(t, fmt.Sprintf(`{"TenantId":"t1","UserId":"u1","Roles":[%[1]q],"AccountIds":["acctA","acctB"],"ScopedEntityIds":{%[1]q:["acctA"]}}`, security.AUTH_ACCOUNT_ADMIN_ROLE))

	sql, resp := captureRulesSQL(t, ctx, deliveriesRequest("notification_deliveries_v2"))

	assert.Empty(t, resp.Errors)
	assert.Contains(t, sql, "'acctA'")
	assert.NotContains(t, sql, "acctB")
	assert.Contains(t, sql, "kind NOT LIKE 'email:%'")
}

func TestNotificationDeliveries_TenantAdminSeesEmail(t *testing.T) {
	ctx := scopedCtx(t, fmt.Sprintf(`{"TenantId":"t1","UserId":"u1","Roles":[%q]}`, security.AUTH_TENANT_ADMIN_ROLE))

	sql, resp := captureRulesSQL(t, ctx, deliveriesRequest("notification_deliveries_v2"))

	assert.Empty(t, resp.Errors)
	assert.NotContains(t, sql, "email:%")
}

func TestNotificationDeliveries_AnAlertsGrantReadsTheTenantButNoEmail(t *testing.T) {
	ctx := scopedCtx(t, `{"TenantId":"t1","UserId":"u1","Roles":[],"CustomPermissions":{"events:Read":true}}`)

	sql, resp := captureRulesSQL(t, ctx, deliveriesRequest("notification_deliveries_v2"))

	assert.Empty(t, resp.Errors)
	assert.NotContains(t, sql, "account_id IN", "a tenant-wide grant reads every account")
	assert.Contains(t, sql, "kind NOT LIKE 'email:%'")
}

func TestNotificationDeliveries_AggregateIsScopedToo(t *testing.T) {
	ctx := scopedCtx(t, fmt.Sprintf(`{"TenantId":"t1","UserId":"u1","Roles":[%[1]q],"AccountIds":["acctA"],"ScopedEntityIds":{%[1]q:["acctA"]}}`, security.AUTH_ACCOUNT_READ_ADMIN_ROLE))

	sql, resp := captureRulesSQL(t, ctx, QueryRequest{Table: "notification_deliveries_grouping_v2", Limit: 10})

	assert.Empty(t, resp.Errors)
	assert.Contains(t, sql, "'acctA'")
	assert.Contains(t, sql, "kind NOT LIKE 'email:%'")
}

func TestNotificationDeliveries_ANamespaceReaderSeesNoneOfThatAccount(t *testing.T) {
	for name, ctxJSON := range map[string]string{
		"with an alerts grant on another account": fmt.Sprintf(`{"TenantId":"t1","UserId":"u1","Roles":[%[1]q],"AccountIds":["acctA","acctB"],"ScopedEntityIds":{%[1]q:["acctA"]},"ScopedCustomPermissions":{"acctB":{"events:Read":true}}}`, security.AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE),
		"while account admin of another account":  fmt.Sprintf(`{"TenantId":"t1","UserId":"u1","Roles":[%[1]q,%[2]q],"AccountIds":["acctA","acctB"],"ScopedEntityIds":{%[1]q:["acctB"],%[2]q:["acctA"]}}`, security.AUTH_ACCOUNT_ADMIN_ROLE, security.AUTH_K8S_NAMESPACE_ADMIN_ROLE),
	} {
		t.Run(name, func(t *testing.T) {
			sql, resp := captureRulesSQL(t, scopedCtx(t, ctxJSON), deliveriesRequest("notification_deliveries_v2"))

			assert.Empty(t, resp.Errors)
			assert.Regexp(t, `account_id NOT IN \('acctA'\)`, sql)
		})
	}
}

func TestNotificationDeliveries_AnAlertsGrantOnTheAccountStillReadsIt(t *testing.T) {
	ctx := scopedCtx(t, fmt.Sprintf(`{"TenantId":"t1","UserId":"u1","Roles":[%[1]q],"AccountIds":["acctA"],"ScopedEntityIds":{%[1]q:["acctA"]},"ScopedCustomPermissions":{"acctA":{"events:Read":true}}}`, security.AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE))

	sql, resp := captureRulesSQL(t, ctx, deliveriesRequest("notification_deliveries_v2"))

	assert.Empty(t, resp.Errors)
	assert.NotContains(t, sql, "account_id NOT IN")
}

// Runs both tables against Postgres: a send's final attempt only, the Slack workspace URL from its
// integration, state and pre-upgrade rows never, email rows for tenant admins only, and per-destination
// latest status for the Failing destinations strip.
func TestNotificationDeliveries_DB(t *testing.T) {
	url := os.Getenv("APP_DATABASE_URL")
	if url == "" {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatal("REQUIRE_DB_TESTS is set but APP_DATABASE_URL is empty")
		}
		t.Skip("skipping: APP_DATABASE_URL is not set")
	}
	db, err := sqlx.Connect("postgres", url)
	if err != nil {
		if os.Getenv("REQUIRE_DB_TESTS") == "true" {
			t.Fatalf("REQUIRE_DB_TESTS is set but the database is not accessible: %v", err)
		}
		t.Skipf("skipping: database not accessible: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)

	const schema = "zz_notification_deliveries_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE sent_notifications (
		id uuid PRIMARY KEY, created_at timestamp NOT NULL DEFAULT now(), tenant_id uuid NOT NULL, account_id uuid,
		fingerprint text, slack_metadata text, teams_metadata text, dispatch_id uuid, attempt smallint, kind text,
		source text, title text, event_id uuid, platform text, team_id text, destination text, destination_name text,
		message_id text, thread_id text, status text, reason text, error_code text, routed_by jsonb)`)
	mustExec(`CREATE TABLE integrations (id uuid PRIMARY KEY, tenant_id uuid, type text, name text)`)
	mustExec(`CREATE TABLE integration_config_values (id uuid PRIMARY KEY, integration_id uuid, name text, value text)`)

	const (
		tenant  = "00000000-0000-0000-0000-00000000000a"
		acctA   = "00000000-0000-0000-0000-0000000000a1"
		acctB   = "00000000-0000-0000-0000-0000000000a2"
		send    = "00000000-0000-0000-0000-0000000000d1"
		retried = "00000000-0000-0000-0000-000000000001"
		final   = "00000000-0000-0000-0000-000000000002"
		other   = "00000000-0000-0000-0000-000000000003"
		email   = "00000000-0000-0000-0000-000000000004"
		marker  = "00000000-0000-0000-0000-000000000005"
		legacy  = "00000000-0000-0000-0000-000000000006"
	)
	row := func(id, account, kind, platform, destination, status string, attempt int, age string) {
		mustExec(`INSERT INTO sent_notifications (id, tenant_id, account_id, dispatch_id, attempt, kind, platform, team_id,
			destination, message_id, status, created_at)
			VALUES ($1, $2, NULLIF($3, '')::uuid, $4, $5, NULLIF($6, ''), NULLIF($7, ''), 'T1', NULLIF($8, ''), '1700000000.000100',
			$9, now() - $10::interval)`, id, tenant, account, send, attempt, kind, platform, destination, status, age)
	}
	row(retried, acctA, "finding", "slack", "C1", "failed", 1, "2 hours")
	row(final, acctA, "finding", "slack", "C1", "sent", 2, "1 hour")
	row(other, acctB, "finding", "slack", "C2", "failed", 1, "1 hour")
	row(email, acctA, "email:daily_recap", "email", "a@x.io", "sent", 1, "1 hour")
	row(marker, acctA, "remediation_marker", "slack", "", "", 1, "1 hour")
	row(legacy, acctA, "", "", "", "", 1, "1 hour")
	mustExec(`INSERT INTO integrations VALUES ('00000000-0000-0000-0000-0000000000e1', $1, 'slack', 'T1')`, tenant)
	mustExec(`INSERT INTO integration_config_values VALUES ('00000000-0000-0000-0000-0000000000f1',
		'00000000-0000-0000-0000-0000000000e1', 'team_url', 'https://acme.slack.com/')`)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: db}, nil
	})
	list := func(ctx *security.RequestContext) (ids []string, teamURLs map[string]any) {
		request := QueryRequest{
			Table:   "notification_deliveries_v2",
			Columns: fromStringColsToQueryCols([]string{"id", "team_url"}),
			Where:   QueryWhereClause{Binary: BinaryWhereClause{"is_final": {Eq: true}}},
			Limit:   10,
		}
		resp, err := ExecuteQuery(ctx, request)
		require.NoError(t, err)
		require.Empty(t, resp.Errors)
		teamURLs = map[string]any{}
		for _, r := range resp.Rows {
			id := fmt.Sprint(r["id"])
			ids = append(ids, id)
			teamURLs[id] = r["team_url"]
		}
		sort.Strings(ids)
		return ids, teamURLs
	}

	tenantAdmin := scopedCtx(t, fmt.Sprintf(`{"TenantId":%q,"UserId":"u1","Roles":[%q]}`, tenant, security.AUTH_TENANT_ADMIN_ROLE))
	ids, teamURLs := list(tenantAdmin)
	assert.Equal(t, []string{final, other, email}, ids, "the retried attempt, the marker and the pre-upgrade row are left out")
	assert.Equal(t, "https://acme.slack.com/", teamURLs[final])
	assert.Nil(t, teamURLs[email])

	accountAdmin := scopedCtx(t, fmt.Sprintf(`{"TenantId":%[1]q,"UserId":"u1","Roles":[%[2]q],"AccountIds":[%[3]q,%[4]q],"ScopedEntityIds":{%[2]q:[%[3]q]}}`, tenant, security.AUTH_ACCOUNT_ADMIN_ROLE, acctA, acctB))
	ids, _ = list(accountAdmin)
	assert.Equal(t, []string{final}, ids, "their account only, and no email")

	namespaceReader := scopedCtx(t, fmt.Sprintf(`{"TenantId":%[1]q,"UserId":"u1","Roles":[%[2]q],"AccountIds":[%[3]q],"ScopedEntityIds":{%[2]q:[%[3]q]}}`, tenant, security.AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE, acctA))
	ids, _ = list(namespaceReader)
	assert.Empty(t, ids, "deliveries carry no namespace, so a namespace reader sees none of the account's")

	mixed := scopedCtx(t, fmt.Sprintf(`{"TenantId":%[1]q,"UserId":"u1","Roles":[%[2]q,%[3]q],"AccountIds":[%[4]q,%[5]q],"ScopedEntityIds":{%[2]q:[%[5]q],%[3]q:[%[4]q]}}`, tenant, security.AUTH_ACCOUNT_ADMIN_ROLE, security.AUTH_K8S_NAMESPACE_READ_ADMIN_ROLE, acctA, acctB))
	ids, _ = list(mixed)
	assert.Equal(t, []string{other}, ids, "the account they administer, not the one they read some namespaces of")

	failing, err := ExecuteQuery(tenantAdmin, QueryRequest{
		Table:   "notification_deliveries_grouping_v2",
		Columns: fromStringColsToQueryCols([]string{"destination", "last_status", "count"}),
		Where:   QueryWhereClause{Binary: BinaryWhereClause{"is_final": {Eq: true}, "platform": {Eq: "slack"}}},
		GroupBy: []string{"destination"},
		Having:  QueryWhereClause{Binary: BinaryWhereClause{"last_status": {Eq: "failed"}}},
		Limit:   10,
	})
	require.NoError(t, err)
	require.Empty(t, failing.Errors)
	require.Len(t, failing.Rows, 1, "C1's latest send went through on its retry")
	assert.Equal(t, "C2", failing.Rows[0]["destination"])
}
