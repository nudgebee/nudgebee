package dashboard

import (
	"encoding/json"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/query"
)

// asMap parses a literal the way the gateway delivers one — as a decoded map,
// not raw bytes. PanelTarget.Query is a map for exactly that reason.
func asMap(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad test fixture %q: %v", body, err)
	}
	return out
}

func TestValidateEntityQuery(t *testing.T) {
	if err := ValidateEntityQuery(DatasourceNudgebee, asMap(t, `{"table":"events_v2","columns":[{"name":"title"}]}`)); err != nil {
		t.Fatalf("expected a valid events query, got %v", err)
	}
	if err := ValidateEntityQuery(DatasourceNudgebee, asMap(t, `{"table":"event_groupings_v2","columns":[{"name":"event_count"}]}`)); err != nil {
		t.Fatalf("expected the grouping table to be allowed, got %v", err)
	}

	// The shape the frontend actually sends: a filter object nested inside the
	// query. This is what broke when the field was a []byte.
	nested := asMap(t, `{"table":"events_v2","columns":[{"name":"title"}],
		"where":{"_and":[{"_binary":{"priority":{"_in":["P0","P1"]}}}]},
		"order_by":[{"column":"starts_at","order":"desc"}],"limit":100}`)
	if err := ValidateEntityQuery(DatasourceNudgebee, nested); err != nil {
		t.Fatalf("expected a filtered query to decode, got %v", err)
	}

	// The engine's registry holds ~111 tables. A panel definition is authored by
	// one user and rendered by every viewer, so anything outside the allowlist —
	// especially the admin/user tables — must be refused here regardless of what
	// the engine's own RBAC would do.
	for _, table := range []string{"admin_get_users_v2", "user_auth_by_username_v2", "tenant_v2", "spends_v2", ""} {
		q := map[string]any{"table": table, "columns": []map[string]string{{"name": "id"}}}
		if err := ValidateEntityQuery(DatasourceNudgebee, q); err == nil {
			t.Errorf("table %q: expected rejection, got nil", table)
		}
	}

	if err := ValidateEntityQuery(DatasourceNudgebee, asMap(t, `{"table":"events_v2"}`)); err == nil {
		t.Error("expected a query with no columns to be rejected")
	}
	if err := ValidateEntityQuery(DatasourceNudgebee, nil); err == nil {
		t.Error("expected a missing query to be rejected")
	}
	// A value the engine's own types cannot hold — `columns` is a list of
	// objects, not a string.
	if err := ValidateEntityQuery(DatasourceNudgebee, map[string]any{"table": "events_v2", "columns": "title"}); err == nil {
		t.Error("expected a malformed query to be rejected")
	}
}

func TestValidateEntityQuery_TablesAreScopedToTheDatasource(t *testing.T) {
	traces := asMap(t, `{"table":"traces_groupings_v2","columns":[{"name":"count"}]}`)
	spans := asMap(t, `{"table":"traces_v2","columns":[{"name":"span_name"}]}`)
	events := asMap(t, `{"table":"events_v2","columns":[{"name":"title"}]}`)
	recommendations := asMap(t, `{"table":"recommendations_v2","columns":[{"name":"rule_name"}]}`)
	recommendationGroups := asMap(t, `{"table":"recommendation_groupings_v2","columns":[{"name":"count"}]}`)

	for _, q := range []map[string]any{traces, spans} {
		if err := ValidateEntityQuery(DatasourceTraces, q); err != nil {
			t.Errorf("expected a traces panel to read %v, got %v", q["table"], err)
		}
	}

	// Recommendations sit alongside events on the nudgebee datasource — same
	// row/aggregate pairing, same account scoping.
	for _, q := range []map[string]any{events, recommendations, recommendationGroups} {
		if err := ValidateEntityQuery(DatasourceNudgebee, q); err != nil {
			t.Errorf("expected a nudgebee panel to read %v, got %v", q["table"], err)
		}
	}
	if err := ValidateEntityQuery(DatasourceTraces, recommendations); err == nil {
		t.Error("expected a traces panel to be refused the recommendations table")
	}

	// A panel says what it is; the tables it may reach follow from that. Without
	// this an events panel could quietly read spans and vice versa.
	if err := ValidateEntityQuery(DatasourceTraces, events); err == nil {
		t.Error("expected a traces panel to be refused the events table")
	}
	if err := ValidateEntityQuery(DatasourceNudgebee, traces); err == nil {
		t.Error("expected an events panel to be refused the traces table")
	}

	// Datasources that do not read the query engine at all.
	for _, datasource := range []string{DatasourceMetrics, DatasourceLogs, DatasourceRedis, ""} {
		if err := ValidateEntityQuery(datasource, events); err == nil {
			t.Errorf("datasource %q: expected rejection, got nil", datasource)
		}
	}
}

func TestRowsToQueryResult(t *testing.T) {
	columns := []query.QueryColumn{{Name: "title"}, {Name: "priority"}, {Name: "count"}}
	rows := []query.QueryRow{
		{"title": "OOMKilled", "priority": "P0", "count": float64(3)},
		{"title": "CrashLoop", "priority": nil, "count": float64(1.5)},
	}
	got := rowsToQueryResult(columns, rows)

	// Column order comes from the REQUEST — Go map iteration is random, so
	// reading it off the row would reshuffle the table on every refresh.
	if len(got.Columns) != 3 || got.Columns[0] != "title" || got.Columns[2] != "count" {
		t.Fatalf("columns = %v", got.Columns)
	}
	if got.Rows[0][0] != "OOMKilled" || got.Rows[0][1] != "P0" {
		t.Errorf("first row = %v", got.Rows[0])
	}
	// A whole number arrives as float64 through JSON; "3" reads better than "3.0".
	if got.Rows[0][2] != "3" {
		t.Errorf("integer cell = %q", got.Rows[0][2])
	}
	if got.Rows[1][1] != "" {
		t.Errorf("null cell = %q, want empty", got.Rows[1][1])
	}
	if got.Rows[1][2] != "1.5" {
		t.Errorf("float cell = %q", got.Rows[1][2])
	}
}

func TestFormatCell(t *testing.T) {
	cases := []struct {
		value any
		want  string
	}{
		{nil, ""},
		{"text", "text"},
		{true, "true"},
		{float64(42), "42"},
		{float64(0.25), "0.25"},
		{[]byte("bytes"), "bytes"},
		// JSON columns (labels, evidences, score_factors) must not render as
		// Go's map syntax.
		{map[string]any{"app": "api"}, `{"app":"api"}`},
	}
	for _, tc := range cases {
		if got := formatCell(tc.value); got != tc.want {
			t.Errorf("formatCell(%v) = %q, want %q", tc.value, got, tc.want)
		}
	}
	if got := formatCell(time.Date(2026, 8, 5, 10, 30, 0, 0, time.UTC)); got != "2026-08-05T10:30:00Z" {
		t.Errorf("time cell = %q", got)
	}
}

// The action layer decodes the gateway's payload with mapstructure, not
// encoding/json. A nested JSON object arrives as a map, and a json.RawMessage
// field is a []byte — mapstructure refused it with "source data must be an
// array or slice, got map", so every save of a nudgebee panel failed. This
// replays the real payload through the real decoder.
func TestSaveRequestDecodesAnEntityQuery(t *testing.T) {
	payload := map[string]any{}
	body := `{
      "id": "a55d4536-9824-4304-a5c1-cae6849fe13b",
      "title": "first",
      "definition": {"panels": [{
        "id": 6, "title": "Events", "type": "table", "datasource": "nudgebee",
        "account_type": "K8s", "grid_pos": {"x":0,"y":0,"w":6,"h":8},
        "targets": [{
          "ref_id": "A",
          "query": {
            "table": "events_v2",
            "columns": [{"name":"starts_at"},{"name":"title"}],
            "order_by": [{"column":"starts_at","order":"desc"}],
            "limit": 100
          },
          "time_column": "starts_at"
        }]
      }]}
    }`
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}

	var req SaveRequest
	if err := common.UnmarshalMapToStruct(payload, &req); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	target := req.Definition.Panels[0].Targets[0]
	if target.TimeColumn != "starts_at" {
		t.Errorf("time_column = %q", target.TimeColumn)
	}
	if target.Query["table"] != "events_v2" {
		t.Fatalf("query did not survive the decode: %#v", target.Query)
	}
	// And the decoded query must still satisfy the executor's own rules.
	if err := ValidateDefinition(req.Definition); err != nil {
		t.Errorf("expected the decoded definition to validate, got %v", err)
	}
}

func TestApplyAccountScope(t *testing.T) {
	q := query.QueryRequest{Table: "events_v2"}
	if err := applyAccountScope([]string{"acc-1", "acc-2"}, &q); err != nil {
		t.Fatalf("scope: %v", err)
	}
	ids, ok := q.Where.And[0].Binary["account_id"][query.In].([]string)
	if !ok || len(ids) != 2 {
		t.Fatalf("account clause = %v", q.Where.And[0].Binary)
	}

	// The K8s and agent tables name the account `cloud_account_id`. Filtering on
	// `account_id` there fails SQL generation with "column not found", so the
	// column is read off the registry rather than assumed.
	nodes := query.QueryRequest{Table: "k8s_nodes_v2"}
	if err := applyAccountScope([]string{"acc-1"}, &nodes); err != nil {
		t.Fatalf("scope k8s_nodes_v2: %v", err)
	}
	if _, ok := nodes.Where.And[0].Binary["cloud_account_id"]; !ok {
		t.Errorf("k8s_nodes_v2 clause = %v, want cloud_account_id", nodes.Where.And[0].Binary)
	}

	// No account means the panel resolved to nothing the viewer can see. Failing
	// closed beats letting a tenant admin read every account.
	for _, requested := range [][]string{nil, {}, {""}} {
		empty := query.QueryRequest{Table: "events_v2"}
		if err := applyAccountScope(requested, &empty); err == nil {
			t.Errorf("accounts %v: expected rejection", requested)
		}
	}

	// A table with no account column cannot be scoped, and must fail rather than
	// run unscoped.
	unknown := query.QueryRequest{Table: "not_a_table_v2"}
	if err := applyAccountScope([]string{"acc-1"}, &unknown); err == nil {
		t.Error("expected an unregistered table to be refused")
	}
}

// Every table an entity panel may reach has to be scopable to an account: the
// executor appends that filter to EVERY query it runs, so a table without an
// account column would 500 at render rather than at save.
//
// Checked across every datasource, not just the executable one. Only nudgebee
// reaches applyAccountScope today — entityQueryExecutable refuses traces, which
// the browser reads through the traces service instead — so the traces rows are
// a guard rather than live coverage: the day that gate opens, a table that
// cannot be scoped should fail here and not in production.
func TestEveryAllowedTableCanBeScopedToAnAccount(t *testing.T) {
	for datasource, tables := range entityQueryTables {
		for table := range tables {
			column, err := accountColumn(table)
			if err != nil {
				t.Errorf("%s/%s: %v", datasource, table, err)
				continue
			}
			def, _ := query.GetTableMetadata(table)
			// The engine compares against a column KEY, not the physical column, so
			// naming one that is not in Columns silently drops the account filter.
			if _, ok := def.Columns[column]; !ok {
				t.Errorf("%s/%s: account column %q is not one of its columns", datasource, table, column)
			}
		}
	}
}

/*
Every executable panel table must name a PermissionModule.

A custom-role holder with no built-in role reaches the query engine's
account-restriction block, where a table with no module has no branch that can
admit it — the read is denied outright and no grant exists to fix it. The panel
editor greys such a table out and names the grant to ask for
(app/src/components/k8s/dashboards/panelAccess.ts), so a module-less table would
put a table in the picker that nobody can be given access to.

Traces are excluded: they are validated here but read through the traces
service, not the engine (see entityQueryExecutable).
*/
func TestEveryExecutableTableNamesAPermissionModule(t *testing.T) {
	for datasource, tables := range entityQueryTables {
		if !entityQueryExecutable[datasource] {
			continue
		}
		for table := range tables {
			def, ok := query.GetTableMetadata(table)
			if !ok {
				t.Errorf("%s/%s: not in the query metadata registry", datasource, table)
				continue
			}
			if def.PermissionModule == "" {
				t.Errorf("%s/%s: no PermissionModule — a custom-role holder can never be granted this table", datasource, table)
			}
		}
	}
}

// The engine's `_between` takes a MAP of bound operators. A [from, to] list
// panics its SQL generator on an unchecked type assertion, which reaches the
// browser as a 500 with an empty body.
func TestTimeRangeClause(t *testing.T) {
	start := time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC).UnixMilli()
	end := time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC).UnixMilli()

	clause, ok := timeRangeClause("starts_at", start, end)
	if !ok {
		t.Fatal("expected a clause")
	}
	bounds, isMap := clause.Binary["starts_at"][query.Between].(map[string]any)
	if !isMap {
		t.Fatalf("_between must be a map, got %T", clause.Binary["starts_at"][query.Between])
	}
	if bounds["_gte"] != "2026-08-05 10:00:00" || bounds["_lte"] != "2026-08-05 11:00:00" {
		t.Errorf("bounds = %v", bounds)
	}

	// Opted out, or nothing to filter on.
	for _, tc := range []struct {
		column     string
		start, end int64
	}{
		{"", start, end},
		{"starts_at", 0, end},
		{"starts_at", start, 0},
	} {
		if _, ok := timeRangeClause(tc.column, tc.start, tc.end); ok {
			t.Errorf("timeRangeClause(%q, %d, %d): expected no clause", tc.column, tc.start, tc.end)
		}
	}
}

func TestMsToUTC(t *testing.T) {
	if got := msToUTC(time.Date(2026, 8, 5, 10, 30, 0, 0, time.UTC).UnixMilli()); got != "2026-08-05 10:30:00" {
		t.Errorf("msToUTC = %q", got)
	}
}
