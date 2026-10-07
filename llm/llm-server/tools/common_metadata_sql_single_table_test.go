package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Queries that read anything but the allowed table, or that the keyword
// count cannot vouch for, are refused (#39880).
func TestSqlCheckSingleTable_Refuses(t *testing.T) {
	cases := map[string]string{
		"union":                             "select id from events where false union all select id from users",
		"subquery in select list":           "select (select password from users limit 1) from events",
		"subquery in where":                 "select * from events where exists (select 1 from user_auths)",
		"second mention reads raw table":    "select * from events where fingerprint in (select fingerprint from events)",
		"cte":                               "with x as (select * from users) select * from events",
		"comma list":                        "select * from events, users",
		"comma after a function item":       "select * from events, generate_series(1,2) g, users",
		"join":                              "select * from events e join users u on true",
		"lateral":                           "select * from events, lateral users",
		"only":                              "select * from only users",
		"select distinct from":              "select distinct from users where password like 'a%'",
		"schema-qualified table":            "select * from pg_catalog.pg_authid",
		"quoted table":                      `select * from "users"`,
		"quoted allowed name":               `select * from "events"`,
		"block comment":                     "select * from events /* hidden */",
		"line comment ended by CR":          "select * from events -- x\r union select * from users",
		"fake clause word in a comment":     "select * from events /* where */, users",
		"table command":                     "select * from events union table users",
		"query_to_xml":                      "select query_to_xml('select * from users', true, true, '') from events",
		"ts_stat":                           "select ts_stat('select tsv from users') from events",
		"schema-qualified function":         "select pgbouncer.get_auth('postgres') from events",
		"aws_s3 export":                     "select aws_s3.query_export_to_s3('select * from users', 'b', 'k') from events",
		"app function":                      "select execute_three_account_queries() from events",
		"pg_sleep":                          "select pg_sleep(100) from events",
		"no from":                           "select 1",
		"ts_rewrite runs sql from a string": "select ts_rewrite('a'::tsquery, 'select q, s from users') from events",
		"crosstab":                          "select crosstab('select password from users') from events",
		"table query in a string":           "select query_to_xml('table users', true, true, '') from events",
		"hex literal glued to union":        "select * from events where id = 0x1funion select id from users",
		"unknown function":                  "select pg_catalog_free_function(id) from events",
		"another app function":              "select session_user_id() from events",
		"timestamp with time zone":          "select starts_at::timestamp with time zone from events",
		"pg_get_viewdef":                    "select pg_get_viewdef('users'::regclass) from events",
		"pg_stat_get_activity":              "select pg_stat_get_activity(null) from events",
		"pg_logical_slot":                   "select pg_logical_slot_peek_changes('s', null, null) from events",
		"advisory lock":                     "select pg_advisory_lock(1) from events",
		"dollar quote hiding a quote":       "select 'a', $$ ' $$, (select 1 from users) from events",
		"dollar quote":                      "select $$x$$ from events",
		"placeholder":                       "select '__nb_metastore_table__' from events",
		"digit glued to union":              "select id from events where x = 1union select 1",
		"literal does not hide a join":      "select * from events, 'x' join users on true",
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, sqlCheckSingleTable(q, "events"), q)
		})
	}
}

func TestSqlCheckSingleTable_AllowsOneTableQueries(t *testing.T) {
	cases := map[string]string{
		"filter sort limit":    "SELECT id, title FROM events WHERE starts_at >= '2026-10-05T08:20:00Z' ORDER BY starts_at DESC LIMIT 20",
		"alias":                "select e.id from events e where e.priority = 'HIGH'",
		"aggregate":            "select subject_type, count(*) from events where status = 'FIRING' group by subject_type order by 2 desc",
		"in list":              "select * from events where priority in ('HIGH', 'CRITICAL')",
		"functions":            "select date_trunc('hour', starts_at), count(*) from events group by 1",
		"json operators":       "select evidences::jsonb->>'type' from events where id = '951f56df-479b-431e-905d-4b8e95214cc1'",
		"window function":      "select id, row_number() over (partition by fingerprint order by starts_at) from events",
		"filter clause":        "select count(*) filter (where priority = 'HIGH') from events",
		"no where":             "select count(*) from events",
		"trailing semicolon":   "select * from events;",
		"keyword in a literal": "select * from events where title ilike '%with join from union%'",
		"quote in a literal":   "select * from events where title = 'it''s from x'",
		// The structured event tools build these queries themselves.
		"json path in a literal": "SELECT jsonb_path_query(evidences::jsonb, '$.**.http_response') FROM events WHERE id = 'x'",
		// Literals are blanked before every check, so these values are fine.
		"literal with table":    "select count(*) from events where subject_namespace in ('data-table')",
		"literal with json":     `select * from events where labels::jsonb @> '{"severity":"critical"}'`,
		"literal with comment":  "select * from events where title ilike '%--%'",
		"date_part for extract": "select date_part('epoch', ends_at - starts_at) from events",
		"having with parens":    "select subject_type, count(*) from events group by subject_type having (count(*) > 5)",
		"within group":          "select percentile_cont(0.9) within group (order by computed_score) from events",
		"math functions":        "select round(power(computed_score, 2)::numeric, 2), mod(event_count, 7) from events",
		"get_event_by_id":       "SELECT id, title, evidences FROM events WHERE id = '951f56df-479b-431e-905d-4b8e95214cc1'",
		"list_events":           "SELECT id, title, starts_at, priority, source, aggregation_key, finding_type, subject_type, subject_name, subject_namespace, nb_status, computed_priority, computed_score, event_count FROM events ORDER BY starts_at DESC",
		"list evidence":         "SELECT id, evidences FROM events",
		"aggregate_events":      "SELECT subject_type, count(DISTINCT fingerprint) AS count FROM events GROUP BY subject_type ORDER BY count DESC",
	}
	for name, q := range cases {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, sqlCheckSingleTable(q, "events"), q)
		})
	}
}

// A tool with a table accepts only a SELECT on it.
func TestSqlValidateReadOnly_OnlySelectWithAnAllowedTable(t *testing.T) {
	for _, q := range []string{"DESC events", "DESCRIBE events", "SHOW COLUMNS FROM events", "WITH x AS (SELECT 1) SELECT * FROM events"} {
		assert.Error(t, sqlValidateReadOnly(q, "events"), q)
	}
}
