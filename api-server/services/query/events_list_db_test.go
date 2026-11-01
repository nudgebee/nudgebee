package query

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventsListIncidentColumns_DB runs the events_v2 SQL against a real
// Postgres. Two things about it cannot be checked from the generated text.
//
// What the incident columns mean is decided in SQL: a child linked to two
// leaders reports the lowest, a leader's member count is distinct alerts rather
// than links, and an incident_leader_id filter — which the def rewrites into a
// join against the link table — has to select exactly the rows the plain filter
// on the column would.
//
// And the reason the columns are select-list subqueries is a property of the
// plan, not of the text: Postgres runs them for the rows of the page, after the
// sort and the limit. The last subtest reads that off EXPLAIN ANALYZE, so a
// change that makes them run for every event in scope again fails here rather
// than as a slow list.
//
// Builds its own tables in a throwaway schema (the convention
// TestNotificationRuleScope_DB set). DB-gated: skips when no database is
// reachable.
func TestEventsListIncidentColumns_DB(t *testing.T) {
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

	const schema = "zz_events_list_incident_columns_test"
	mustExec := func(q string, args ...any) {
		_, e := db.Exec(q, args...)
		require.NoError(t, e, q)
	}
	drop := func() { _, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`) }
	drop()
	t.Cleanup(drop)
	mustExec(`CREATE SCHEMA ` + schema)
	mustExec(`SET search_path TO ` + schema)
	mustExec(`CREATE TABLE events (
		id uuid PRIMARY KEY, tenant uuid NOT NULL, cloud_account_id uuid NOT NULL,
		created_at timestamp NOT NULL, fingerprint text, aggregation_key text,
		computed_score int, labels jsonb)`)
	mustExec(`CREATE TABLE event_correlations (
		event_id uuid NOT NULL, related_event_id uuid NOT NULL, cloud_account_id uuid NOT NULL,
		correlation_type text NOT NULL)`)
	mustExec(`CREATE TABLE event_log_analysis (
		event_fingerprint text, cloud_account_id uuid, event_aggregation_key text,
		analysis_type text, status text, recorded_at timestamp, updated_at timestamp)`)

	const (
		tenant  = "00000000-0000-0000-0000-0000000000aa"
		account = "00000000-0000-0000-0000-0000000000a1"
		// Leaders sort leaderLow < leaderHigh; "lowest leader wins" depends on it.
		leaderLow  = "00000000-0000-0000-0000-000000000101"
		leaderHigh = "00000000-0000-0000-0000-000000000102"
		childLow   = "00000000-0000-0000-0000-000000000201" // linked to leaderLow
		childBoth  = "00000000-0000-0000-0000-000000000202" // linked to both leaders
		childHigh  = "00000000-0000-0000-0000-000000000203" // linked to leaderHigh
		analysed   = "00000000-0000-0000-0000-000000000301" // all four stages completed
		partial    = "00000000-0000-0000-0000-000000000302" // three of four stages
		reopened   = "00000000-0000-0000-0000-000000000303" // newest row of one stage in progress
		fillers    = 60
		eventStamp = "2026-01-10 12:00:00"
	)
	event := func(id, fingerprint string, score int) {
		mustExec(`INSERT INTO events (id, tenant, cloud_account_id, created_at, fingerprint, aggregation_key, computed_score, labels)
			VALUES ($1, $2, $3, $4::timestamp + make_interval(secs => $5::int), $6, 'PodCrashLooping', $5::int, '{"team":"shop"}')`,
			id, tenant, account, eventStamp, score, fingerprint)
	}
	link := func(child, leader string) {
		mustExec(`INSERT INTO event_correlations VALUES ($1, $2, $3, 'same_incident')`, child, leader, account)
	}
	stage := func(fingerprint, analysisType, status, at string) {
		mustExec(`INSERT INTO event_log_analysis VALUES ($1, $2, 'PodCrashLooping', $3, $4, $5, $5)`,
			fingerprint, account, analysisType, status, at)
	}

	// Scores 1..60 for the fillers, so the named events below outrank none of them.
	for i := 1; i <= fillers; i++ {
		event(fmt.Sprintf("00000000-0000-0000-0000-%012d", 9000+i), fmt.Sprintf("filler-%d", i), i)
	}
	event(leaderLow, "leader-low", 0)
	event(leaderHigh, "leader-high", 0)
	// childLow and childBoth are firings of one alert; childHigh is another alert.
	event(childLow, "alert-a", 0)
	event(childBoth, "alert-a", 0)
	event(childHigh, "alert-c", 0)
	link(childLow, leaderLow)
	link(childBoth, leaderHigh)
	link(childBoth, leaderLow)
	link(childHigh, leaderHigh)
	// A link of another type must not count as incident membership.
	mustExec(`INSERT INTO event_correlations VALUES ($1, $2, $3, 'temporal')`, analysed, leaderLow, account)

	event(analysed, "fp-analysed", 0)
	event(partial, "fp-partial", 0)
	event(reopened, "fp-reopened", 0)
	for _, analysisType := range []string{"summary", "investigation", "log_analysis", "detailed_response"} {
		stage("fp-analysed", analysisType, "COMPLETED", eventStamp)
		stage("fp-reopened", analysisType, "COMPLETED", eventStamp)
		if analysisType != "detailed_response" {
			stage("fp-partial", analysisType, "COMPLETED", eventStamp)
		}
	}
	stage("fp-reopened", "summary", "IN_PROGRESS", "2026-01-10 13:00:00")

	td, ok := GetTableMetadata("events_v2")
	require.True(t, ok)
	listSQL := func(where BinaryWhereClause, orderBy string, limit int) string {
		if where == nil {
			where = BinaryWhereClause{}
		}
		where["tenant_id"] = map[BinaryWhereClauseType]any{Eq: tenant}
		where["account_id"] = map[BinaryWhereClauseType]any{Eq: account}
		generated, genErr := GenerateSqlQuery(superAdminCtx(), "", QueryRequest{
			Table:   "events_v2",
			Columns: cols("id", "labels", "computed_score", "incident_leader_id", "incident_member_count", "is_investigated"),
			Where:   QueryWhereClause{Binary: where},
			OrderBy: []QueryOrderBy{{Column: orderBy, Order: "desc"}},
			Limit:   limit,
		}, td)
		require.NoError(t, genErr)
		return generated
	}
	type row struct {
		ID           string         `db:"id"`
		Labels       string         `db:"labels"`
		Score        int            `db:"computed_score"`
		Leader       sql.NullString `db:"incident_leader_id"`
		MemberCount  int            `db:"incident_member_count"`
		Investigated bool           `db:"is_investigated"`
	}
	list := func(where BinaryWhereClause, orderBy string, limit int) map[string]row {
		var rows []row
		require.NoError(t, db.Select(&rows, listSQL(where, orderBy, limit)))
		byID := make(map[string]row, len(rows))
		for _, r := range rows {
			byID[r.ID] = r
		}
		return byID
	}
	ids := func(rows map[string]row) []string {
		out := make([]string, 0, len(rows))
		for id := range rows {
			out = append(out, id)
		}
		sort.Strings(out)
		return out
	}

	t.Run("leader, member count and investigated flag", func(t *testing.T) {
		all := list(nil, "created_at", 1000)
		require.Len(t, all, fillers+8)

		assert.Equal(t, leaderLow, all[childLow].Leader.String)
		assert.Equal(t, leaderLow, all[childBoth].Leader.String, "a child linked to two leaders reports the lowest")
		assert.Equal(t, leaderHigh, all[childHigh].Leader.String)
		assert.False(t, all[leaderLow].Leader.Valid)
		assert.False(t, all[analysed].Leader.Valid, "only same_incident links make an event a child")

		assert.Equal(t, 1, all[leaderLow].MemberCount, "two firings of one alert are one member")
		assert.Equal(t, 2, all[leaderHigh].MemberCount)
		assert.Equal(t, 0, all[childLow].MemberCount)

		assert.True(t, all[analysed].Investigated)
		assert.False(t, all[partial].Investigated, "all four stages have to be completed")
		assert.False(t, all[reopened].Investigated, "the newest row of each stage decides")
		assert.False(t, all[leaderLow].Investigated)

		assert.JSONEq(t, `{"team":"shop"}`, all[childLow].Labels)
	})

	t.Run("folded list leaves out the children", func(t *testing.T) {
		folded := list(BinaryWhereClause{"incident_leader_id": {IsNull: true}}, "created_at", 1000)
		require.Len(t, folded, fillers+5)
		for _, child := range []string{childLow, childBoth, childHigh} {
			assert.NotContains(t, folded, child)
		}
		assert.Equal(t, 2, folded[leaderHigh].MemberCount)
	})

	t.Run("children only", func(t *testing.T) {
		children := list(BinaryWhereClause{"incident_leader_id": {IsNull: false}}, "created_at", 1000)
		assert.Equal(t, []string{childLow, childBoth, childHigh}, ids(children))
	})

	t.Run("members of one incident", func(t *testing.T) {
		assert.Equal(t, []string{childLow, childBoth},
			ids(list(BinaryWhereClause{"incident_leader_id": {Eq: leaderLow}}, "created_at", 1000)))
		assert.Equal(t, []string{childHigh},
			ids(list(BinaryWhereClause{"incident_leader_id": {Eq: leaderHigh}}, "created_at", 1000)),
			"the child linked to both is listed under its lowest leader only")
	})

	t.Run("a page sorted by score holds the top scores", func(t *testing.T) {
		page := list(nil, "computed_score", 5)
		scores := make([]int, 0, len(page))
		for _, r := range page {
			scores = append(scores, r.Score)
		}
		sort.Ints(scores)
		assert.Equal(t, []int{56, 57, 58, 59, 60}, scores)
	})

	t.Run("the lookups run for the page, not for every event in scope", func(t *testing.T) {
		const pageSize = 5
		// computed_score has no index here, so Postgres has to sort all 68
		// events itself; created_at is covered for completeness.
		for _, orderBy := range []string{"computed_score", "created_at"} {
			var raw string
			require.NoError(t, db.Get(&raw, `EXPLAIN (ANALYZE, FORMAT JSON) `+listSQL(nil, orderBy, pageSize)))
			var plans []struct {
				Plan map[string]any `json:"Plan"`
			}
			require.NoError(t, json.Unmarshal([]byte(raw), &plans))
			require.Len(t, plans, 1)

			lookups := 0
			var walk func(node map[string]any)
			walk = func(node map[string]any) {
				if relation, _ := node["Relation Name"].(string); relation == "event_correlations" || relation == "event_log_analysis" {
					lookups++
					assert.LessOrEqual(t, node["Actual Loops"].(float64), float64(pageSize),
						"sorted by %s: %s was read once per event in scope instead of once per row of the page", orderBy, relation)
				}
				children, _ := node["Plans"].([]any)
				for _, child := range children {
					walk(child.(map[string]any))
				}
			}
			walk(plans[0].Plan)
			assert.Equal(t, 3, lookups, "sorted by %s: expected one lookup each for the leader, the member count and the investigated flag", orderBy)
		}
	})
}
