package dashboard

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/query"
	"nudgebee/services/security"
)

/*
Entity-query panels read the internal query engine rather than an observability
provider.

The engine exposes ~111 tables, and most of them have nothing to do with
dashboards — `admin_get_users_v2`, `user_auth_by_username_v2`, `tenant_v2`. Its
own per-table RBAC still applies, but a panel definition is authored by one user
and rendered by every viewer, so the reachable surface is pinned to an explicit
allowlist here instead of being "whatever the registry happens to contain".
*/
// Keyed by datasource so a `traces` panel cannot read events and vice versa —
// the panel says what it is, and the tables it may reach follow from that.
var entityQueryTables = map[string]map[string]bool{
	DatasourceNudgebee: {
		// Rows: one event per row.
		"events_v2": true,
		// The aggregate twin, for counts (event_count, count_priority_p0 …). The
		// engine will not take an arbitrary GROUP BY on events_v2.
		"event_groupings_v2": true,
		// Rows: one recommendation per row. Same row/aggregate pairing as the
		// events tables, and the same account scoping — both expose `account_id`
		// (mapped to cloud_account_id), which is what applyAccountScope filters on.
		"recommendations_v2": true,
		// The aggregate twin, for counts and summed savings.
		"recommendation_groupings_v2": true,

		// Spend, for the cost widgets. Recommendations say what could be saved;
		// only this says what was actually billed, which is the difference
		// between a waste dashboard and a cost dashboard.
		"spend_groupings_v2": true,
		// Per-cluster capacity and pod/workload counts, one row per account.
		"k8s_cluster_groupings_v2": true,
		// Node rows behind that summary — capacity, flavor, spot, pod count.
		// Scopes on `cloud_account_id`, not `account_id`; see accountColumn.
		"k8s_nodes_v2": true,
		// Ticket inflow and backlog, grouped by status / assignee / platform.
		"ticket_groupings_v2": true,
		// Metric and spend anomalies: the grouped feed and the rows behind it.
		"anomaly_grouping_v2": true,
		"anomaly_v2":          true,
		// Posture: CIS rule violations grouped by rule, and the image/workload
		// vulnerability rows.
		"recommendation_security_cis_groupings_v2": true,
		"recommendation_security_v2":               true,
		// Automation: autopilot task runs by state, and the approvals waiting on
		// a human.
		"auto_pilot_task_groupings_v2": true,
		"auto_pilot_approvals_v2":      true,
		// Governance: who changed what, and whether the agents are still talking
		// to us. Both carry their own PermissionModule / account scoping in the
		// engine, so a viewer sees only the tenant and accounts they may read.
		"audits_v2":           true,
		"get_agent_health_v2": true,
		// AI investigation throughput, grouped by source and status.
		"llm_conversation_groupings_v2": true,
	},
	DatasourceTraces: {
		// One span per row.
		"traces_v2": true,
		// Pre-aggregated: count, error_count, p95/p99 latency.
		"traces_groupings_v2": true,
	},
}

// IsEntityDatasource reports whether a panel's data comes from the internal
// query engine rather than from a provider or a relay command.
func IsEntityDatasource(datasource string) bool {
	_, ok := entityQueryTables[datasource]
	return ok
}

/*
Datasources this action will EXECUTE.

Traces are validated here — a stored panel names one of the trace tables — but
they are read through the traces service from the browser, not through the query
engine. That service is what resolves an account's span store (its Elasticsearch
index, its ClickHouse source); the engine's own trace tables resolve their source
from a top-level `account_id: {_eq: …}` and answer against the default otherwise.
Rather than reimplement that lookup, this refuses to run them.
*/
var entityQueryExecutable = map[string]bool{
	DatasourceNudgebee: true,
}

const (
	defaultEntityQueryLimit = 100
	maxEntityQueryLimit     = 500
)

// ValidateEntityQuery accepts only a query this feature can actually run. Used
// at save; ExecuteEntityQuery repeats it, since a stored panel and a direct
// caller both reach the executor without passing through save.
func ValidateEntityQuery(datasource string, raw map[string]any) error {
	_, err := decodeEntityQuery(datasource, raw)
	return err
}

/*
decodeEntityQuery turns the stored map into the engine's request type.

Round-tripped through JSON rather than mapstructure so the `json` tags on
QueryRequest are what govern — the where clause is a nest of maps keyed by
operator, and its shape is defined by those tags.
*/
func decodeEntityQuery(datasource string, raw map[string]any) (query.QueryRequest, error) {
	var q query.QueryRequest
	tables, ok := entityQueryTables[datasource]
	if !ok {
		return q, fmt.Errorf("datasource %q does not read the query engine", datasource)
	}
	body, err := json.Marshal(raw)
	if err != nil {
		return q, fmt.Errorf("query is not readable: %w", err)
	}
	if err := json.Unmarshal(body, &q); err != nil {
		return q, fmt.Errorf("query is not readable: %w", err)
	}
	if !tables[q.Table] {
		return q, fmt.Errorf("%q is not a table a %s panel can query", q.Table, datasource)
	}
	if len(q.Columns) == 0 {
		return q, fmt.Errorf("select at least one column")
	}
	return q, nil
}

/*
ExecuteEntityQuery runs one entity-query panel.

Account and time filters are appended HERE rather than being stored in the
panel: the accounts come from the panel's scope (and the viewer's filter), and
the window comes from the dashboard's time picker, so neither is a property of
the saved query. The caller is responsible for having checked account access.
*/
func ExecuteEntityQuery(ctx *security.RequestContext, req EntityQueryRequest) (*QueryResult, error) {
	if !entityQueryExecutable[req.Datasource] {
		return nil, fmt.Errorf("%s panels are not read through the query engine", req.Datasource)
	}
	q, err := decodeEntityQuery(req.Datasource, req.Query)
	if err != nil {
		return nil, err
	}
	if q.Limit <= 0 || q.Limit > maxEntityQueryLimit {
		q.Limit = defaultEntityQueryLimit
	}

	if err := applyAccountScope(req.AccountIds, &q); err != nil {
		return nil, err
	}

	// Unlike a metrics panel, a row query carries no notion of a time range —
	// without this the panel would ignore the dashboard's time picker entirely.
	if clause, ok := timeRangeClause(req.TimeColumn, req.StartTime, req.EndTime); ok {
		q.Where.And = append(q.Where.And, clause)
	}

	resp, err := query.ExecuteQuery(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("%s", resp.Errors[0].Message)
	}
	return rowsToQueryResult(q.Columns, resp.Rows), nil
}

/*
applyAccountScope narrows the query to the accounts the panel resolved to.

This narrows; it never widens. The engine appends its own account restriction
from the caller's RBAC regardless of what arrives here.
*/
func applyAccountScope(requested []string, q *query.QueryRequest) error {
	// An empty account list is NOT "every account": the panel resolved to
	// nothing the viewer can see, and the engine would answer tenant-wide for a
	// tenant admin. Refuse rather than widen.
	accountIds := nonEmptyStrings(requested)
	if len(accountIds) == 0 {
		return fmt.Errorf("no account is in scope for this panel")
	}
	column, err := accountColumn(q.Table)
	if err != nil {
		return err
	}
	q.Where.And = append(q.Where.And, query.QueryWhereClause{
		Binary: query.BinaryWhereClause{column: {query.In: accountIds}},
	})
	return nil
}

/*
accountColumn is the column a table names its owning account with.

Most of the allowlist says `account_id`, but the K8s and agent tables say
`cloud_account_id` — and the engine compares against the column NAME, so
assuming `account_id` fails SQL generation with "column not found" rather than
returning the wrong rows. Read off the registry so the two can never drift.
*/
func accountColumn(table string) (string, error) {
	def, ok := query.GetTableMetadata(table)
	if !ok {
		return "", fmt.Errorf("%q is not a table the query engine knows", table)
	}
	if def.AccountIdColumnName == "" {
		return "", fmt.Errorf("%q has no account column, so a panel cannot be scoped to one", table)
	}
	return def.AccountIdColumnName, nil
}

/*
timeRangeClause expresses the dashboard's window as the engine's `_between`.

`_between` takes a MAP of bound operators — `{"_gte": from, "_lte": to}` — not a
[from, to] pair. Passing a list panics the SQL generator on an unchecked type
assertion (query/sql.go, `value.(map[string]any)`), which surfaces as a 500 with
an empty body rather than an error message.

Returns false when the panel opted out of the time range, or when the caller
sent no window at all.
*/
func timeRangeClause(column string, startMs, endMs int64) (query.QueryWhereClause, bool) {
	column = strings.TrimSpace(column)
	if column == "" || startMs <= 0 || endMs <= 0 {
		return query.QueryWhereClause{}, false
	}
	return query.QueryWhereClause{
		Binary: query.BinaryWhereClause{
			column: {query.Between: map[string]any{"_gte": msToUTC(startMs), "_lte": msToUTC(endMs)}},
		},
	}, true
}

// msToUTC converts the epoch milliseconds the frontend sends into the timestamp
// literal the engine's dialects expect.
func msToUTC(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04:05")
}

/*
rowsToQueryResult flattens the engine's rows into the same table shape the
command datasources produce, so one renderer serves both.

Column ORDER comes from the request, not from the row map — Go map iteration is
random, so reading the order off the response would shuffle the table's columns
on every refresh.
*/
func rowsToQueryResult(columns []query.QueryColumn, rows []query.QueryRow) *QueryResult {
	names := make([]string, len(columns))
	for i, c := range columns {
		names[i] = c.Name
	}
	// Bounded by capRows below, so the capacity never exceeds the cap even when
	// the engine returns far more.
	result := &QueryResult{Columns: names, Rows: make([][]string, 0, min(len(rows), maxQueryRows))}
	for _, row := range rows {
		cells := make([]string, len(names))
		for i, name := range names {
			cells[i] = formatCell(row[name])
		}
		result.Rows = append(result.Rows, cells)
	}
	return capRows(result)
}

// formatCell renders one value as display text. The panel table is strings —
// see QueryResult — and these rows carry timestamps, JSON columns and NULLs.
func formatCell(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		// Whole numbers arrive as float64 through JSON; 3 reads better than 3.
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case time.Time:
		return v.UTC().Format(time.RFC3339)
	case []byte:
		return string(v)
	default:
		// JSON columns (labels, evidences, score_factors) land here.
		if encoded, err := json.Marshal(v); err == nil {
			return string(encoded)
		}
		return fmt.Sprintf("%v", v)
	}
}
