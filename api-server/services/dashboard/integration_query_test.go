package dashboard

import (
	"strings"
	"testing"
)

func TestValidatePanelCommand_AllowsReadOnlyCommands(t *testing.T) {
	cases := []struct {
		datasource string
		command    string
	}{
		{DatasourceRedis, "INFO"},
		{DatasourceRedis, "INFO memory"},
		{DatasourceRedis, "info replication"}, // case-insensitive
		{DatasourceRedis, "DBSIZE"},
		{DatasourceRedis, "CONFIG GET maxmemory"},
		{DatasourceRedis, "CLIENT LIST"},
		{DatasourceRedis, "SLOWLOG GET 10"},
		{DatasourceRabbitMQ, "list queues"},
		{DatasourceRabbitMQ, "list queues name messages consumers"},
		{DatasourceRabbitMQ, "show overview"},
	}
	for _, tc := range cases {
		if err := ValidatePanelCommand(tc.datasource, tc.command); err != nil {
			t.Errorf("%s %q: expected allowed, got %v", tc.datasource, tc.command, err)
		}
	}
}

func TestValidatePanelCommand_RejectsWrites(t *testing.T) {
	// The point of the allowlist: a dashboard is authored by one user and run by
	// every viewer, so a panel must never be able to mutate or drain anything.
	cases := []struct {
		datasource string
		command    string
	}{
		{DatasourceRedis, "FLUSHALL"},
		{DatasourceRedis, "DEL mykey"},
		{DatasourceRedis, "SET k v"},
		{DatasourceRedis, "KEYS *"}, // read, but O(n) blocking — not on the list
		{DatasourceRedis, "CONFIG SET maxmemory 0"},
		{DatasourceRedis, "CLIENT KILL ID 4"},
		{DatasourceRedis, "SHUTDOWN"},
		{DatasourceRabbitMQ, "delete queue name=work"},
		{DatasourceRabbitMQ, "purge queue name=work"},
		{DatasourceRabbitMQ, "publish routing_key=q payload=hi"},
		{DatasourceRabbitMQ, "close connection name=c"},
		// rabbitmqadmin is case-sensitive, so an upper-case verb is not the same
		// command and must not be waved through by a case-insensitive match.
		{DatasourceRabbitMQ, "LIST queues"},
	}
	for _, tc := range cases {
		if err := ValidatePanelCommand(tc.datasource, tc.command); err == nil {
			t.Errorf("%s %q: expected rejection, got nil", tc.datasource, tc.command)
		}
	}
}

func TestValidatePanelCommand_PostgresAllowsReads(t *testing.T) {
	for _, sql := range []string{
		"SELECT 1",
		"select count(*) from pg_stat_activity",
		// Single quotes are ordinary SQL and must survive — they are inside
		// psql's double-quoted -c argument, so they cannot escape it.
		"SELECT state, count(*) FROM pg_stat_activity WHERE state = 'active' GROUP BY state",
		"WITH recent AS (SELECT * FROM pg_stat_activity) SELECT count(*) FROM recent",
		"SHOW max_connections",
		"EXPLAIN SELECT 1",
		"TABLE pg_stat_database",
	} {
		if err := ValidatePanelCommand(DatasourcePostgres, sql); err != nil {
			t.Errorf("%q: expected allowed, got %v", sql, err)
		}
	}
}

func TestValidatePanelCommand_PostgresRejectsWrites(t *testing.T) {
	cases := []struct {
		sql    string
		reason string
	}{
		{"DELETE FROM users", "bare write"},
		{"UPDATE users SET name = 'x'", "bare write"},
		{"INSERT INTO users VALUES (1)", "bare write"},
		{"DROP TABLE users", "bare write"},
		{"TRUNCATE users", "bare write"},
		{"CREATE TABLE t (id int)", "bare write"},
		{"GRANT ALL ON users TO public", "privilege change"},
		{"VACUUM FULL", "maintenance"},
		// Postgres allows data-modifying CTEs, so a statement that OPENS with an
		// allowed keyword can still write. This is why write verbs are rejected
		// anywhere, not only at the start.
		{"WITH gone AS (DELETE FROM users RETURNING *) SELECT * FROM gone", "data-modifying CTE"},
		// EXPLAIN ANALYZE executes the statement rather than only planning it.
		{"EXPLAIN ANALYZE SELECT 1", "executes"},
		// SELECT … INTO creates a table and fills it, and opens with an ALLOWED
		// prefix — the same shape of bypass as the data-modifying CTE above.
		{"SELECT * INTO stolen FROM users", "SELECT INTO creates a table"},
		{"SELECT id INTO TEMP t FROM users", "SELECT INTO, temp table"},
		// Two statements in one -c: psql runs both.
		{"SELECT 1; DROP TABLE users", "statement chaining"},
		{"COPY users TO '/tmp/x'", "writes a file"},
		{"SET search_path = evil", "session state"},
	}
	for _, tc := range cases {
		if err := ValidatePanelCommand(DatasourcePostgres, tc.sql); err == nil {
			t.Errorf("%q (%s): expected rejection, got nil", tc.sql, tc.reason)
		}
	}
}

func TestValidatePanelCommand_PostgresRejectsShellEscapes(t *testing.T) {
	// The statement is embedded in psql's -c "…", so anything that could close
	// that string or expand inside it has to go.
	for _, sql := range []string{
		`SELECT "quoted_column" FROM t`,
		"SELECT 1 `whoami`",
		"SELECT $PGPASSWORD",
		"SELECT $$dollar quoted$$",
		"SELECT 1 > /tmp/out",
		"SELECT 1\\",
		"SELECT 1\nSELECT 2",
	} {
		if err := ValidatePanelCommand(DatasourcePostgres, sql); err == nil {
			t.Errorf("%q: expected rejection, got nil", sql)
		}
	}
}

func TestValidatePanelCommand_PostgresRequiresAReadingStatement(t *testing.T) {
	if err := ValidatePanelCommand(DatasourcePostgres, "pg_dump"); err == nil {
		t.Error("expected a non-SELECT statement to be rejected")
	}
	if err := ValidatePanelCommand(DatasourcePostgres, "   "); err == nil {
		t.Error("expected an empty statement to be rejected")
	}
}

func TestBuildShellCommand(t *testing.T) {
	// psql takes the statement as ONE argument, so it is quoted; the other two
	// take ordinary words and must not be.
	got := buildShellCommand(DatasourcePostgres, "SELECT 1")
	if got != `psql -A -F '|' -c "SELECT 1"` {
		t.Errorf("postgres command = %q", got)
	}
	if got := buildShellCommand(DatasourceRedis, "INFO memory"); !strings.HasSuffix(got, " INFO memory") {
		t.Errorf("redis command = %q", got)
	}
	if got := buildShellCommand(DatasourceRabbitMQ, "list queues"); !strings.HasSuffix(got, " list queues") {
		t.Errorf("rabbitmq command = %q", got)
	}
}

func TestParsePsqlOutput(t *testing.T) {
	got := parsePsqlOutput("state|count\nactive|3\nidle|11\n(2 rows)\n")
	if len(got.Columns) != 2 || got.Columns[0] != "state" || got.Columns[1] != "count" {
		t.Fatalf("columns = %v", got.Columns)
	}
	// The footer is psql's, not a row.
	if len(got.Rows) != 2 || got.Rows[1][0] != "idle" || got.Rows[1][1] != "11" {
		t.Fatalf("rows = %v", got.Rows)
	}

	// A short row must not index out of range.
	short := parsePsqlOutput("a|b|c\n1|2\n(1 row)")
	if len(short.Rows[0]) != 3 || short.Rows[0][2] != "" {
		t.Errorf("short row = %v", short.Rows[0])
	}

	// A psql error has no header row; showing it verbatim beats splitting it
	// into nonsense columns.
	failed := parsePsqlOutput(`ERROR:  relation "nope" does not exist`)
	if failed.Columns[0] != "Output" || !strings.HasPrefix(failed.Rows[0][0], "ERROR:") {
		t.Errorf("error output = %+v", failed)
	}

	if empty := parsePsqlOutput(""); len(empty.Rows) != 0 {
		t.Errorf("empty output = %+v", empty)
	}
}

func TestValidateDefinition_PostgresPanel(t *testing.T) {
	def := Definition{Panels: []Panel{{
		Id:         1,
		Title:      "connections by state",
		Type:       VizTable,
		Datasource: DatasourcePostgres,
		AccountIds: []string{"acc-1"},
		GridPos:    GridPos{W: 6, H: 8},
		Targets:    []PanelTarget{{RefId: "A", Expr: "SELECT state, count(*) FROM pg_stat_activity GROUP BY state"}},
	}}}
	if err := ValidateDefinition(def); err != nil {
		t.Fatalf("expected a valid postgres panel, got %v", err)
	}

	def.Panels[0].Targets[0].Expr = "DELETE FROM users"
	if err := ValidateDefinition(def); err == nil {
		t.Error("expected a write statement to be rejected at save")
	}
}

func TestValidatePanelCommand_RejectsShellEscapes(t *testing.T) {
	// The panel supplies only the arguments after a credentialed prefix this
	// package builds. Anything that could terminate that command and start
	// another has to be refused.
	for _, command := range []string{
		"INFO; FLUSHALL",
		"INFO && redis-cli FLUSHALL",
		"INFO | tee /tmp/x",
		"INFO `whoami`",
		"INFO $(whoami)",
		"INFO $REDIS_PASSWORD",
		"INFO > /tmp/out",
		"INFO\nFLUSHALL",
		`INFO "quoted"`,
		"INFO 'quoted'",
		"INFO \\; FLUSHALL",
	} {
		if err := ValidatePanelCommand(DatasourceRedis, command); err == nil {
			t.Errorf("%q: expected rejection, got nil", command)
		}
	}
}

func TestValidatePanelCommand_RejectsEmptyOversizedAndUnknownDatasource(t *testing.T) {
	if err := ValidatePanelCommand(DatasourceRedis, "   "); err == nil {
		t.Error("expected empty command to be rejected")
	}
	if err := ValidatePanelCommand(DatasourceRedis, "INFO "+strings.Repeat("a", maxCommandLength)); err == nil {
		t.Error("expected oversized command to be rejected")
	}
	// metrics panels do not run commands; asking to run one is a coding error,
	// not a fallthrough to "allowed".
	if err := ValidatePanelCommand(DatasourceMetrics, "INFO"); err == nil {
		t.Error("expected a non-command datasource to be rejected")
	}
}

func TestValidateDefinition_CommandPanels(t *testing.T) {
	build := func(mutate func(*Panel)) Definition {
		p := Panel{
			Id:         1,
			Title:      "redis memory",
			Type:       VizTable,
			Datasource: DatasourceRedis,
			AccountIds: []string{"acc-1"},
			GridPos:    GridPos{W: 6, H: 8},
			Targets:    []PanelTarget{{RefId: "A", Expr: "INFO memory"}},
		}
		mutate(&p)
		return Definition{Panels: []Panel{p}}
	}

	if err := ValidateDefinition(build(func(*Panel) {})); err != nil {
		t.Fatalf("expected a valid redis table panel, got %v", err)
	}

	// A snapshot of text has nothing to plot, so the chart types are refused at
	// save rather than rendering an empty chart.
	for _, viz := range []string{VizTimeseries, VizStat, VizGauge, VizBar} {
		if err := ValidateDefinition(build(func(p *Panel) { p.Type = viz })); err == nil {
			t.Errorf("expected %s to be rejected for a command datasource", viz)
		}
	}

	// Save-time validation must run the same allowlist the executor does,
	// otherwise the rejection only surfaces on someone else's screen at render.
	if err := ValidateDefinition(build(func(p *Panel) { p.Targets[0].Expr = "FLUSHALL" })); err == nil {
		t.Error("expected a write command to be rejected at save")
	}
}

func TestParseRedisOutput(t *testing.T) {
	info := "# Memory\r\nused_memory:1024\r\nused_memory_human:1.00K\r\n\r\n# Stats\r\ntotal_connections_received:7\r\n"
	got := parseRedisOutput(info)
	if want := []string{"Key", "Value"}; got.Columns[0] != want[0] || got.Columns[1] != want[1] {
		t.Fatalf("columns = %v", got.Columns)
	}
	// Section headers and blank lines carry no data.
	if len(got.Rows) != 3 {
		t.Fatalf("rows = %v", got.Rows)
	}
	if got.Rows[0][0] != "used_memory" || got.Rows[0][1] != "1024" {
		t.Errorf("first row = %v", got.Rows[0])
	}

	// Line-oriented output (PING, DBSIZE) has no key:value shape to split on.
	plain := parseRedisOutput("PONG")
	if len(plain.Columns) != 1 || plain.Columns[0] != "Output" || plain.Rows[0][0] != "PONG" {
		t.Errorf("plain output = %+v", plain)
	}
}

func TestParseRabbitmqOutput(t *testing.T) {
	table := `+---------+----------+
|  name   | messages |
+---------+----------+
| work    | 12       |
| retries | 0        |
+---------+----------+`
	got := parseRabbitmqOutput(table)
	if len(got.Columns) != 2 || got.Columns[0] != "name" || got.Columns[1] != "messages" {
		t.Fatalf("columns = %v", got.Columns)
	}
	if len(got.Rows) != 2 || got.Rows[0][0] != "work" || got.Rows[0][1] != "12" {
		t.Fatalf("rows = %v", got.Rows)
	}

	// An error or a non-table response must still reach the user verbatim
	// rather than rendering as an empty table.
	fallback := parseRabbitmqOutput("Error: not authorized")
	if fallback.Columns[0] != "Output" || fallback.Rows[0][0] != "Error: not authorized" {
		t.Errorf("fallback = %+v", fallback)
	}
}

func TestCapRows(t *testing.T) {
	rows := make([][]string, maxQueryRows+10)
	for i := range rows {
		rows[i] = []string{"x"}
	}
	got := capRows(&QueryResult{Columns: []string{"Output"}, Rows: rows})
	if len(got.Rows) != maxQueryRows || !got.Truncated {
		t.Errorf("rows = %d truncated = %v", len(got.Rows), got.Truncated)
	}
	// Under the cap nothing is flagged — a panel must not claim truncation it
	// did not perform.
	small := capRows(&QueryResult{Columns: []string{"Output"}, Rows: [][]string{{"x"}}})
	if small.Truncated {
		t.Error("expected truncated = false")
	}
}
