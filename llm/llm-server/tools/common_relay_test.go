package tools

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestUnwrapCLIWrappedQuery covers the workspace-shim unwrapping path added to
// ExecuteContainerJob. The workspace pod's shim posts back commands that look
// like `sqlcmd -d "db" -Q "SQL" -s "\t" -W` (and psql/mariadb/sqlplus variants).
// Older forager builds (pre-2026-03-27, commit 5754003) do not strip this
// wrapping, so the raw flags reach MSSQL/PG and produce errors like
// "Incorrect syntax near 'Q'" / "'d'". This test pins the unwrap behaviour we
// depend on to make the fix version-independent of the forager binary.
func TestUnwrapCLIWrappedQuery(t *testing.T) {
	tests := []struct {
		name    string
		module  RelayJob
		input   string
		wantSQL string
		wantDB  string
	}{
		{
			name:    "sqlcmd with -d reproduces customer bug (d error)",
			module:  RelayJobMssql,
			input:   `sqlcmd -d "master" -Q "SELECT 1" -s "	" -W`,
			wantSQL: "SELECT 1",
			wantDB:  "master",
		},
		{
			name:    "sqlcmd without -d (Q error)",
			module:  RelayJobMssql,
			input:   `sqlcmd -Q "SELECT 1" -s "	" -W`,
			wantSQL: "SELECT 1",
			wantDB:  "",
		},
		{
			name:    "sqlcmd complex query with single quotes inside",
			module:  RelayJobMssql,
			input:   `sqlcmd -d "msdb" -Q "SELECT j.name FROM msdb.dbo.sysjobs j WHERE j.name = 'testfailure'" -s "	" -W`,
			wantSQL: "SELECT j.name FROM msdb.dbo.sysjobs j WHERE j.name = 'testfailure'",
			wantDB:  "msdb",
		},
		{
			name:    "sqlcmd with escaped double quotes inside SQL",
			module:  RelayJobMssql,
			input:   `sqlcmd -Q "SELECT * FROM t WHERE col = \"value\""`,
			wantSQL: `SELECT * FROM t WHERE col = "value"`,
			wantDB:  "",
		},
		{
			name:    "psql plain -c with --dbname",
			module:  RelayJobPostgres,
			input:   `psql --dbname mydb -c "SELECT 1"`,
			wantSQL: "SELECT 1",
			wantDB:  "mydb",
		},
		{
			name:    "psql copy form (CSV)",
			module:  RelayJobPostgres,
			input:   `psql -c "\copy (SELECT name FROM users) TO stdout WITH CSV HEADER"`,
			wantSQL: "SELECT name FROM users",
			wantDB:  "",
		},
		{
			name:    "mariadb -e",
			module:  RelayJobMysql,
			input:   `mariadb --user=u --password=p -e "SELECT 1"`,
			wantSQL: "SELECT 1",
			wantDB:  "",
		},
		{
			name:    "sqlplus -Q (workspace convention)",
			module:  RelayJobOracle,
			input:   `sqlplus -d "ORCL" -Q "SELECT sysdate FROM dual"`,
			wantSQL: "SELECT sysdate FROM dual",
			wantDB:  "ORCL",
		},
		{
			name:    "already raw SQL passthrough",
			module:  RelayJobMssql,
			input:   "SELECT 1",
			wantSQL: "SELECT 1",
			wantDB:  "",
		},
		{
			name:    "raw SQL with legitimate -d inside string literal",
			module:  RelayJobMssql,
			input:   "SELECT 'abc -d xyz' FROM t",
			wantSQL: "SELECT 'abc -d xyz' FROM t",
			wantDB:  "",
		},
		{
			// Guard against false positive: `-d` appears inside the SQL
			// payload of a sqlcmd-wrapped query. The database extraction must
			// scan only the portion before `-Q`, otherwise we'd pull a stray
			// token out of the string literal.
			name:    "sqlcmd with spurious -d inside SQL payload",
			module:  RelayJobMssql,
			input:   `sqlcmd -Q "SELECT col FROM t WHERE col = ' -d ' AND x = 1"`,
			wantSQL: "SELECT col FROM t WHERE col = ' -d ' AND x = 1",
			wantDB:  "",
		},
		{
			// Guard against cross-module mismatch: a raw MSSQL query whose
			// literal happens to contain "psql" should not be mistaken for a
			// psql-wrapped query just because module was RelayJobPostgres.
			name:    "module mismatch: sqlcmd-wrapped query passed as Postgres module",
			module:  RelayJobPostgres,
			input:   `sqlcmd -d "master" -Q "SELECT 1"`,
			wantSQL: `sqlcmd -d "master" -Q "SELECT 1"`,
			wantDB:  "",
		},
		{
			// The workspace shim (code-analysis/cmd/shim/main.go) re-serializes
			// os.Args with POSIX single-quoting when an arg contains any of
			// ` \t\n'"` etc. So what arrives at /api/v1/workspace/execute is
			// single-quoted, NOT the double-quoted form tool_mssql.go emitted.
			// Failing to handle this made MSSQL see `'SELECT` (first
			// space-delimited token) and return "Unclosed quotation mark after
			// the character string 'SELECT'".
			name:    "shim-quoted sqlcmd (single quotes around SQL, no -d)",
			module:  RelayJobMssql,
			input:   `sqlcmd -Q 'SELECT TABLE_CATALOG, TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE FROM INFORMATION_SCHEMA.TABLES ORDER BY TABLE_SCHEMA, TABLE_NAME' -s '	' -W`,
			wantSQL: "SELECT TABLE_CATALOG, TABLE_SCHEMA, TABLE_NAME, TABLE_TYPE FROM INFORMATION_SCHEMA.TABLES ORDER BY TABLE_SCHEMA, TABLE_NAME",
			wantDB:  "",
		},
		{
			name:    "shim-quoted sqlcmd with -d",
			module:  RelayJobMssql,
			input:   `sqlcmd -d 'master' -Q 'SELECT 1' -s '	' -W`,
			wantSQL: "SELECT 1",
			wantDB:  "master",
		},
		{
			// POSIX shell trick for a literal single quote: 'foo'\''bar' -> foo'bar
			name:    "shim-quoted sqlcmd with single-quote in SQL payload",
			module:  RelayJobMssql,
			input:   `sqlcmd -Q 'SELECT j.name FROM msdb.dbo.sysjobs j WHERE j.name = '\''testfailure'\''' -s '	' -W`,
			wantSQL: "SELECT j.name FROM msdb.dbo.sysjobs j WHERE j.name = 'testfailure'",
			wantDB:  "",
		},
		{
			name:    "shim-quoted psql with --dbname",
			module:  RelayJobPostgres,
			input:   `psql --dbname 'mydb' -c 'SELECT 1'`,
			wantSQL: "SELECT 1",
			wantDB:  "mydb",
		},
		{
			name:    "shim-quoted sqlplus with -Q",
			module:  RelayJobOracle,
			input:   `sqlplus -d 'ORCL' -Q 'SELECT sysdate FROM dual'`,
			wantSQL: "SELECT sysdate FROM dual",
			wantDB:  "ORCL",
		},
		{
			name:    "shim-quoted mariadb -e",
			module:  RelayJobMysql,
			input:   `mariadb --user=u --password=p -e 'SELECT 1'`,
			wantSQL: "SELECT 1",
			wantDB:  "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotSQL, gotDB := unwrapCLIWrappedQuery(tc.input, tc.module)
			assert.Equal(t, tc.wantSQL, gotSQL, "SQL mismatch")
			assert.Equal(t, tc.wantDB, gotDB, "database mismatch")
		})
	}
}

// TestSshShellQuote pins the helper that single-quotes the user's command
// before ssh transmission. Without it the workspace pod's local shell
// re-parses the command and silently mangles `$VAR`, `$1`, `$(...)`,
// backticks — observed in the wild as `awk '{print $1}'` becoming
// `awk '{print }'`. The same helper exists in relay-server's
// `workspace.go` and api-server's `ssh.go`; this test guards the
// llm-server copy against drift from those.
func TestSshShellQuote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "''"},
		{"plain", "uname -a", "'uname -a'"},
		{"awk_field_ref", `awk '{print $1}' /tmp/log`, `'awk '\''{print $1}'\'' /tmp/log'`},
		{"command_substitution", `grep "$(cat /tmp/x)" /tmp/log`, `'grep "$(cat /tmp/x)" /tmp/log'`},
		{"dollar_var", "echo $HOME", "'echo $HOME'"},
		{"backticks", "echo `whoami`", "'echo `whoami`'"},
		{"single_quote_inside", `echo 'hello'`, `'echo '\''hello'\'''`},
		{"double_quotes_inside", `echo "hello"`, `'echo "hello"'`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, sshShellQuote(c.in))
		})
	}
}

// TestSSHOverrideRegexes pins the regex behaviour that gates caller-supplied
// host_name / user_name in the RelayJobSSH case of ExecuteContainerJob. These
// values are interpolated into a shell command, so any pattern that lets a
// metacharacter through is a command-injection bug. The same metacharacter
// set is also covered end-to-end in
// `relay-server/pkg/server/handlers/workspace_ssh_test.go`; this test guards
// the llm-server duplicate so it can't drift independently.
func TestSSHOverrideRegexes(t *testing.T) {
	goodHosts := []string{"1.2.3.4", "sadservers-vm.example.com", "host-1", "a.b.c", "localhost"}
	for _, h := range goodHosts {
		assert.True(t, sshHostRe.MatchString(h), "good host rejected: %q", h)
	}

	badHosts := []string{
		"",
		"1.2.3.4; rm -rf /",
		"`whoami`",
		"$(id)",
		"host name",
		"host\nrm -rf /",
		"-leading-dash",
		"host..example.com",
	}
	for _, h := range badHosts {
		assert.False(t, sshHostRe.MatchString(h), "bad host accepted: %q", h)
	}

	goodUsers := []string{"admin", "ec2-user", "ubuntu", "root", "user.name", "_svc"}
	for _, u := range goodUsers {
		assert.True(t, sshUserRe.MatchString(u), "good user rejected: %q", u)
	}

	badUsers := []string{"", "admin;ls", "root user", "$(id)", "-flag", "1user"}
	for _, u := range badUsers {
		assert.False(t, sshUserRe.MatchString(u), "bad user accepted: %q", u)
	}
}

// TestGetRelayCommandResponseData_LegacyFindingsShape pins the
// deeply-nested findings/evidence/data structure used by the analysis
// path. Walks the full chain and asserts the inner response payload is
// returned to the caller.
func TestGetRelayCommandResponseData_LegacyFindingsShape(t *testing.T) {
	// Inner shape: findings[0].evidence[0].data is a JSON-encoded array
	// (carried as a Go string), element 0's `data` is itself a
	// JSON-encoded object (also a Go string) holding the command
	// response. The walker unwraps both layers via json.Unmarshal.
	innerCmdResponseJSON := `{"response":"pod/app-1 created\n","exit_code":0}`
	innerArrayJSON := `[{"data":` + strconv.Quote(innerCmdResponseJSON) + `}]`
	relayResp := map[string]any{
		"data": map[string]any{
			"findings": []any{
				map[string]any{
					"evidence": []any{
						map[string]any{
							"data": innerArrayJSON,
						},
					},
				},
			},
		},
	}

	got, err := getRelayCommandResponseData(relayResp)
	assert.NoError(t, err)
	assert.Equal(t, "pod/app-1 created\n", got["response"])
}

// TestGetRelayCommandResponseData_SimpleDataShape covers issue #32240:
// command-style executions (kubectl, helm, etc.) return a relay response
// without the findings envelope. Before this fix the parser hard-errored
// with "findings field not found or is nil from data", which the shim
// surfaced to the LLM as the opaque "Server returned 500" error that
// affected ~10–15% of kubectl_execute calls in production.
//
// New contract: when data.findings is absent, data itself is the
// already-extracted payload and is returned as-is so the caller's
// responseParsed["response"] lookup can proceed.
func TestGetRelayCommandResponseData_SimpleDataShape(t *testing.T) {
	relayResp := map[string]any{
		"data": map[string]any{
			"response": "deployment.apps/app-dev REVISION  CHANGE-CAUSE\n1  <none>\n2  <none>\n",
			"command":  "kubectl rollout history deployment/app-dev -n nudgebee",
		},
	}
	got, err := getRelayCommandResponseData(relayResp)
	assert.NoError(t, err)
	assert.Equal(t, "deployment.apps/app-dev REVISION  CHANGE-CAUSE\n1  <none>\n2  <none>\n", got["response"])
	assert.Equal(t, "kubectl rollout history deployment/app-dev -n nudgebee", got["command"])
}

// TestGetRelayCommandResponseData_EmptyFindings preserves the
// "ran but produced nothing" path — empty findings list returns an empty
// map, not an error.
func TestGetRelayCommandResponseData_EmptyFindings(t *testing.T) {
	relayResp := map[string]any{
		"data": map[string]any{
			"findings": []any{},
		},
	}
	got, err := getRelayCommandResponseData(relayResp)
	assert.NoError(t, err)
	assert.Empty(t, got)
}

// TestGetRelayCommandResponseData_MissingData asserts the parser still
// errors when even the outer `data` field is absent — we only relaxed
// the findings requirement, not the data requirement.
func TestGetRelayCommandResponseData_MissingData(t *testing.T) {
	relayResp := map[string]any{
		"status": "error",
	}
	_, err := getRelayCommandResponseData(relayResp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "data1 field not found")
}

// TestGetRelayCommandResponseData_NilData covers the explicit nil case.
func TestGetRelayCommandResponseData_NilData(t *testing.T) {
	relayResp := map[string]any{
		"data": nil,
	}
	_, err := getRelayCommandResponseData(relayResp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "data1 field not found")
}
