package dashboard

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
)

// A panel command runs on the customer's cluster, so the command a viewer ends
// up executing is text that some OTHER user typed into a dashboard. Three rules
// keep that from being a remote-shell:
//
//  1. The credentialed prefix (`redis-cli -h $REDIS_HOST …`) is built HERE. The
//     panel supplies only the arguments after it, so it can never rewrite the
//     host, the auth flags, or the binary.
//  2. Anything that could end the command and start another — quoting, shell
//     metacharacters, newlines — is rejected outright.
//  3. The verb must be on a read-only allowlist. `CONFIG GET` is allowed;
//     `CONFIG SET`, `FLUSHALL` and `rabbitmqadmin delete queue` are not.
//
// Validation runs both at save (so the author gets told immediately) and again
// at execute — execute is the authoritative one, because rows written before a
// rule existed, and callers that hit the action directly, never pass through
// save.
const (
	redisCommandPrefix    = "redis-cli -h $REDIS_HOST --user $REDIS_USER --pass $REDIS_PASSWORD --no-auth-warning"
	rabbitmqCommandPrefix = "rabbitmqadmin --host $RABBITMQ_HOST --port $RABBITMQ_PORT --username $RABBITMQ_USER --password $RABBITMQ_PASSWORD"
	// psql reads PGHOST/PGUSER/PGPASSWORD from the integration's secret, so no
	// connection flags are needed — only output ones. `-A -F '|'` is the
	// unaligned pipe-separated format (present in every psql version, unlike
	// --csv), which parses without guessing at column widths.
	postgresCommandPrefix = `psql -A -F '|' -c`

	// maxCommandLength is generous for a real read command and small enough that
	// a pasted script is refused rather than executed.
	maxCommandLength = 512
	// maxQueryRows caps what one panel can pull back. `CLIENT LIST` on a busy
	// Redis is thousands of lines, which is neither readable in a panel nor
	// something to push through the gateway on every render.
	maxQueryRows = 500
)

// Characters that could chain, redirect, or substitute another command. `$` is
// included even though the prefix relies on it — the prefix is ours, the panel's
// half must not reach for an environment variable.
var unsafeCommandChars = regexp.MustCompile("[;&|<>`$\\\\\n\r\t\"']")

// SQL is the one command language that legitimately needs quotes — `WHERE state
// = 'active'` is an ordinary query — so single quotes are allowed and the rest
// of the blocklist tightens instead:
//
//   - The statement is embedded in psql's `-c "…"`, so a DOUBLE quote would
//     close that string early. Rejected (and with it `"identifier"` quoting).
//   - `;` is literal to the shell inside double quotes, but psql runs every
//     statement in one -c, so `SELECT 1; DROP TABLE x` would execute both.
//   - `$` still expands inside double quotes, which also rules out $$-quoting.
var unsafeSQLChars = regexp.MustCompile("[;<>`$\\\\\n\r\t\"]")

// Statements that may open a read. Anything else is refused outright.
var readOnlySQLPrefixes = []string{"SELECT", "WITH", "SHOW", "EXPLAIN", "TABLE", "VALUES"}

// Write verbs, rejected wherever they appear rather than only at the start —
// Postgres allows data-modifying CTEs (`WITH x AS (…) DELETE FROM …`), which
// begin with an allowed keyword. `ANALYZE` is here because `EXPLAIN ANALYZE`
// actually runs the statement; plain `EXPLAIN` is the read-only one.
//
// `INTO` is here for the same reason as the CTEs: `SELECT … INTO new_table …`
// starts with an ALLOWED prefix and creates a table, so a prefix check alone
// lets a write through. It costs the false positive of refusing a query with
// the bare word `into` in a string literal — the same trade every other verb in
// this list already makes.
var writeSQLKeywords = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|MERGE|DROP|ALTER|CREATE|TRUNCATE|GRANT|REVOKE|COPY|VACUUM|ANALYZE|REINDEX|CALL|DO|SET|RESET|REFRESH|LOCK|NOTIFY|LISTEN|PREPARE|DEALLOCATE|BEGIN|COMMIT|ROLLBACK|SAVEPOINT|INTO)\b`)

// Read-only verbs per datasource. A nil subcommand list means the verb is safe
// with any arguments; a non-nil one means the second word must be in it, which
// is what separates `CONFIG GET` from `CONFIG SET`.
var (
	redisReadOnlyCommands = map[string][]string{
		"INFO":     nil,
		"DBSIZE":   nil,
		"PING":     nil,
		"LASTSAVE": nil,
		"TIME":     nil,
		"ROLE":     nil,
		"CONFIG":   {"GET"},
		"CLIENT":   {"LIST", "INFO"},
		"MEMORY":   {"STATS", "DOCTOR", "USAGE"},
		"CLUSTER":  {"INFO", "NODES"},
		"LATENCY":  {"LATEST", "HISTORY"},
		"SLOWLOG":  {"GET", "LEN"},
		"COMMAND":  {"COUNT", "DOCS"},
	}
	// rabbitmqadmin's read verbs. Everything else it exposes — declare, delete,
	// purge, publish, get, close, import — mutates or drains.
	rabbitmqReadOnlyCommands = map[string][]string{
		"list": nil,
		"show": nil,
	}
)

// ValidatePanelCommand accepts only a read-only command for a command datasource.
func ValidatePanelCommand(datasource, command string) error {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return fmt.Errorf("a %s panel needs a command", datasource)
	}
	if len(cmd) > maxCommandLength {
		return fmt.Errorf("command is too long (max %d characters)", maxCommandLength)
	}

	if datasource == DatasourcePostgres {
		return validateReadOnlySQL(cmd)
	}

	if unsafeCommandChars.MatchString(cmd) {
		return fmt.Errorf("command may contain only a plain read command — quotes, pipes, redirects and variables are not allowed")
	}

	allowed, normalize, err := commandVocabulary(datasource)
	if err != nil {
		return err
	}
	fields := strings.Fields(cmd)
	verb := normalize(fields[0])

	subcommands, ok := allowed[verb]
	if !ok {
		return fmt.Errorf("%q is not a readable %s command; allowed: %s", fields[0], datasource, strings.Join(sortedKeys(allowed), ", "))
	}
	if len(subcommands) == 0 {
		return nil
	}
	if len(fields) < 2 {
		return fmt.Errorf("%s needs one of: %s", verb, strings.Join(subcommands, ", "))
	}
	sub := strings.ToUpper(fields[1])
	for _, candidate := range subcommands {
		if sub == candidate {
			return nil
		}
	}
	return fmt.Errorf("%s %s is not readable; allowed: %s", verb, fields[1], strings.Join(subcommands, ", "))
}

// validateReadOnlySQL accepts a single read-only statement.
//
// It is a keyword filter, not a parser: a column literally named `update` is
// refused. That is the intended trade — the alternative is embedding a SQL
// parser to decide whether a panel may write to a customer's database.
func validateReadOnlySQL(sql string) error {
	if unsafeSQLChars.MatchString(sql) {
		return fmt.Errorf(`a query may be a single statement with no ";", double quotes, redirects or shell variables (single quotes for values are fine)`)
	}
	upper := strings.ToUpper(sql)
	opens := false
	for _, prefix := range readOnlySQLPrefixes {
		if strings.HasPrefix(upper, prefix+" ") || upper == prefix {
			opens = true
			break
		}
	}
	if !opens {
		return fmt.Errorf("a query must start with %s", strings.Join(readOnlySQLPrefixes, ", "))
	}
	if match := writeSQLKeywords.FindString(sql); match != "" {
		return fmt.Errorf("%q is not allowed — panels may only read", strings.ToUpper(match))
	}
	return nil
}

// commandVocabulary returns the allowlist for a datasource plus how to fold a
// verb's case before looking it up.
func commandVocabulary(datasource string) (map[string][]string, func(string) string, error) {
	switch datasource {
	case DatasourceRedis:
		// Redis accepts either case, so normalise up and match once.
		return redisReadOnlyCommands, strings.ToUpper, nil
	case DatasourceRabbitMQ:
		// rabbitmqadmin is case-SENSITIVE — `LIST queues` is not a command it
		// runs. Matching case-insensitively here would pass validation and then
		// fail at the CLI with a far worse message, so compare verbatim.
		return rabbitmqReadOnlyCommands, func(s string) string { return s }, nil
	default:
		return nil, nil, fmt.Errorf("datasource %q does not run commands", datasource)
	}
}

// ExecuteQuery runs one command-datasource panel against one account.
//
// The caller is responsible for the access check — this reaches into the
// account's cluster, so it must not be called without HasAccountAccess.
func ExecuteQuery(ctx *security.RequestContext, req ExecuteQueryRequest) (*QueryResult, error) {
	if err := ValidatePanelCommand(req.Datasource, req.Command); err != nil {
		return nil, err
	}

	integration, err := core.GetIntegrationByType(ctx, req.AccountId, req.Datasource)
	if err != nil {
		return nil, err
	}
	if integration == nil {
		return nil, fmt.Errorf("no %s integration is connected to this account", req.Datasource)
	}

	// The relay executes inside the cluster and reads credentials from the k8s
	// secret the integration names, so the secret never leaves the cluster and
	// this service never handles the password.
	secretName, err := core.GetIntegrationConfigValueByName(ctx, integration.Id, "k8s_secret")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(secretName) == "" {
		// Redis and Postgres also offer a vm_agent connection mode, which has no
		// k8s secret and no relay command path.
		return nil, fmt.Errorf("the %s integration for this account is not configured with a Kubernetes secret, which is the only mode dashboards can query", req.Datasource)
	}

	resp, err := relay.CommandExecutor(req.AccountId, buildShellCommand(req.Datasource, strings.TrimSpace(req.Command)), secretName, map[string]string{})
	if err != nil {
		return nil, err
	}
	output, ok := resp["response"].(string)
	if !ok {
		return nil, fmt.Errorf("unexpected response from the %s server", req.Datasource)
	}

	switch req.Datasource {
	case DatasourceRabbitMQ:
		return parseRabbitmqOutput(output), nil
	case DatasourcePostgres:
		return parsePsqlOutput(output), nil
	default:
		return parseRedisOutput(output), nil
	}
}

/*
buildShellCommand puts the validated panel command behind its credentialed
prefix.

Only Postgres needs the command QUOTED — psql takes the statement as one `-c`
argument, where redis-cli and rabbitmqadmin take theirs as ordinary words. The
double quotes are safe because validateReadOnlySQL has already refused any `"`,
`$`, backslash or backtick that could escape them.
*/
func buildShellCommand(datasource, command string) string {
	switch datasource {
	case DatasourceRabbitMQ:
		return rabbitmqCommandPrefix + " " + command
	case DatasourcePostgres:
		return fmt.Sprintf(`%s "%s"`, postgresCommandPrefix, command)
	default:
		return redisCommandPrefix + " " + command
	}
}

/*
parsePsqlOutput reads psql's unaligned output:

	id|name
	1|alice
	(1 row)

The `(N rows)` footer is psql's, not data. A value containing the separator
splits wrong — the price of a text protocol, and the reason the separator is
`|` rather than a comma.
*/
func parsePsqlOutput(output string) *QueryResult {
	lines := usefulLines(output, func(line string) bool {
		return psqlFooterRe.MatchString(line)
	})
	if len(lines) == 0 {
		return &QueryResult{Columns: []string{"Output"}}
	}
	// An error from psql arrives on the same channel as a result set and has no
	// header row worth splitting.
	if strings.HasPrefix(lines[0], "ERROR:") || strings.HasPrefix(lines[0], "psql:") {
		result := &QueryResult{Columns: []string{"Output"}}
		for _, line := range lines {
			result.Rows = append(result.Rows, []string{line})
		}
		return capRows(result)
	}

	columns := strings.Split(lines[0], "|")
	for i := range columns {
		columns[i] = strings.TrimSpace(columns[i])
	}
	result := &QueryResult{Columns: columns, Rows: make([][]string, 0, min(len(lines)-1, maxQueryRows))}
	for _, line := range lines[1:] {
		cells := strings.Split(line, "|")
		row := make([]string, len(columns))
		for i := range columns {
			if i < len(cells) {
				row[i] = strings.TrimSpace(cells[i])
			}
		}
		result.Rows = append(result.Rows, row)
	}
	return capRows(result)
}

var psqlFooterRe = regexp.MustCompile(`^\(\d+ rows?\)$`)

// parseRedisOutput tabulates redis-cli output.
//
// `INFO` and `CONFIG GET`-style output is `key:value` per line, which reads as a
// two-column table. Anything else (PING, DBSIZE, CLIENT LIST) is line-oriented
// and is shown as-is rather than split on a delimiter that may not mean
// anything.
func parseRedisOutput(output string) *QueryResult {
	lines := usefulLines(output, func(line string) bool {
		// `# Server` section headers carry no data.
		return strings.HasPrefix(line, "#")
	})
	keyed := 0
	for _, line := range lines {
		if strings.Contains(line, ":") {
			keyed++
		}
	}
	// Mixed output (a stray banner among key:value lines) still reads better as
	// a table, so require only a majority rather than all.
	if len(lines) > 0 && keyed*2 > len(lines) {
		result := &QueryResult{Columns: []string{"Key", "Value"}, Rows: make([][]string, 0, min(len(lines), maxQueryRows))}
		for _, line := range lines {
			key, value, found := strings.Cut(line, ":")
			if !found {
				result.Rows = append(result.Rows, []string{strings.TrimSpace(line), ""})
				continue
			}
			result.Rows = append(result.Rows, []string{strings.TrimSpace(key), strings.TrimSpace(value)})
		}
		return capRows(result)
	}

	result := &QueryResult{Columns: []string{"Output"}, Rows: make([][]string, 0, min(len(lines), maxQueryRows))}
	for _, line := range lines {
		result.Rows = append(result.Rows, []string{line})
	}
	return capRows(result)
}

// parseRabbitmqOutput reads rabbitmqadmin's ASCII table:
//
//	+-------+----------+
//	| name  | messages |
//	+-------+----------+
//	| queue | 0        |
//	+-------+----------+
//
// The first pipe row is the header. Anything that isn't a pipe row (an error
// message, `show overview`'s plain output) falls back to raw lines so the user
// sees what the server actually said instead of an empty table.
func parseRabbitmqOutput(output string) *QueryResult {
	// Parsed once and reused by the fallback below, which used to re-split the
	// whole output a second time to say the same thing.
	lines := usefulLines(output, nil)
	cells := make([][]string, 0, min(len(lines), maxQueryRows))
	for _, line := range lines {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		trimmed := strings.Trim(line, "|")
		parts := strings.Split(trimmed, "|")
		row := make([]string, len(parts))
		for i, part := range parts {
			row[i] = strings.TrimSpace(part)
		}
		cells = append(cells, row)
	}

	if len(cells) == 0 {
		result := &QueryResult{Columns: []string{"Output"}, Rows: make([][]string, 0, min(len(lines), maxQueryRows))}
		for _, line := range lines {
			result.Rows = append(result.Rows, []string{line})
		}
		return capRows(result)
	}
	return capRows(&QueryResult{Columns: cells[0], Rows: cells[1:]})
}

// usefulLines trims and drops blank lines, plus whatever `skip` rejects.
func usefulLines(output string, skip func(string) bool) []string {
	var lines []string
	for _, raw := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		// rabbitmqadmin's +---+ rules are decoration.
		if strings.HasPrefix(line, "+-") {
			continue
		}
		if skip != nil && skip(line) {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func capRows(result *QueryResult) *QueryResult {
	if len(result.Rows) > maxQueryRows {
		result.Rows = result.Rows[:maxQueryRows]
		result.Truncated = true
	}
	return result
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
