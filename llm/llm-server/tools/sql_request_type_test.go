package tools

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/llm/tools/core"
)

func TestInferSQLRequestType(t *testing.T) {
	read, update, unsure := core.ToolRequestTypeRead, core.ToolRequestTypeUpdate, core.ToolRequestType("")
	cases := []struct {
		query string
		want  core.ToolRequestType
	}{
		{"SELECT 1", read},
		{"select * from events where status = 'update' limit 5;", read},
		{`SELECT "comment", "set" FROM t`, read},
		{"SHOW PROCESSLIST;", read},
		{"DESCRIBE orders", read},
		{"WITH x AS (SELECT id FROM t) SELECT * FROM x", read},
		{"EXPLAIN UPDATE insight SET status = 'CLOSED' WHERE id = 1", read},
		{"EXPLAIN (ANALYZE, BUFFERS) SELECT count(*) FROM t", read},
		{"SELECT query FROM pg_stat_statements WHERE query LIKE '%$1%'", read},
		{"select 1; select 2", read},
		{"GRANT pg_read_all_stats TO \"read-user\";", update},
		{"INSERT INTO t VALUES (1)", update},
		{"DELETE FROM t", update},
		{"drop table t", update},
		{"EXPLAIN ANALYZE UPDATE t SET a = 1", update},
		{"EXPLAIN ANALYZE DELETE FROM t", update},
		{"select 1; drop table t", update},
		{"SELECT pg_terminate_backend(123)", update},
		{"select set_config('x', '1', false)", update},
		{"SELECT nextval('s')", update},
		{"SELECT * FROM dblink('host=x', 'select 1') AS t(a int)", update},
		{"ALTER TABLE t DELETE WHERE 1", update},
		{"SYSTEM DROP DNS CACHE", update},
		{"SELECT * INTO new_t FROM t", unsure},
		{"SELECT * FROM t FOR UPDATE", unsure},
		{"WITH d AS (DELETE FROM t RETURNING *) SELECT * FROM d", unsure},
		{"select comment from posts", unsure},
		{"DO $$ BEGIN PERFORM 1; END $$", unsure},
		{"select 'unclosed", unsure},
		{"vacuum analyze t", update},
		{"pragma table_info(t)", unsure},
		{"", unsure},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, inferSQLRequestType(tc.query), tc.query)
	}
}

func TestInferSQLRequestTypeInputShapes(t *testing.T) {
	asJSON := func(fields map[string]string) string {
		b, _ := json.Marshal(fields)
		return string(b)
	}
	assert.Equal(t, core.ToolRequestTypeRead, inferSQLRequestType(asJSON(map[string]string{"query": "SELECT 1"})))
	assert.Equal(t, core.ToolRequestTypeUpdate, inferSQLRequestType(asJSON(map[string]string{"command": "DELETE FROM t", "query": "SELECT 1"})), "command is read first, as the tools do")
	assert.Equal(t, core.ToolRequestType(""), inferSQLRequestType(asJSON(map[string]string{"database": "x"})))
	assert.Equal(t, core.ToolRequestTypeRead, inferSQLRequestType(`mysql -e "SHOW DATABASES"`))
	assert.Equal(t, core.ToolRequestTypeUpdate, inferSQLRequestType(`mysql --execute="DROP DATABASE x"`))
	assert.Equal(t, core.ToolRequestType(""), inferSQLRequestType(`mysql -h db`))
}
