package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/relay"
	"nudgebee/llm/tools/core"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
)

type RelayJob string

const (
	RelayJobShell      RelayJob = "shell"
	RelayJobKubectl    RelayJob = "kubectl"
	RelayJobHelm       RelayJob = "helm"
	RelayJobArgoCD     RelayJob = "argocd"
	RelayJobPostgres   RelayJob = "postgres"
	RelayJobMysql      RelayJob = "mysql"
	RelayJobRabbitmq   RelayJob = "rabbitmq"
	RelayJobRedis      RelayJob = "redis"
	RelayJobClickhouse RelayJob = "clickhouse"
	RelayJobMssql      RelayJob = "mssql"
	RelayJobOracle     RelayJob = "oracle"
	RelayJobSSH        RelayJob = "ssh"
)

func getRelayCommandResponseData(relayResponse map[string]any) (map[string]any, error) {
	data1, ok := relayResponse["data"].(map[string]any)
	if !ok || data1 == nil {
		return nil, errors.New("data1 field not found or is nil from response")
	}
	findings, ok := data1["findings"].([]any)
	if !ok || findings == nil {
		return nil, errors.New("findings field not found or is nil from data")
	}

	if len(findings) == 0 {
		return map[string]any{}, nil
	}

	firstArrayMap := findings[0].(map[string]any)
	evidenceData := firstArrayMap["evidence"]
	if evidenceData == nil {
		return map[string]any{}, nil
	}

	evidenceDataArray := evidenceData.([]any)
	if len(evidenceDataArray) == 0 {
		return map[string]any{}, nil
	}

	firstEvidence := evidenceDataArray[0].(map[string]any)

	firstEvidenceData := firstEvidence["data"]

	if firstEvidenceData == nil {
		return map[string]any{}, nil
	}

	commandResponseArray := []any{}
	err := common.UnmarshalJson([]byte(firstEvidenceData.(string)), &commandResponseArray)
	if err != nil {
		return nil, err
	}

	if len(commandResponseArray) == 0 {
		return map[string]any{}, nil
	}

	firstResponse := commandResponseArray[0].(map[string]any)

	firstResponseData := firstResponse["data"]

	if firstResponseData == nil {
		return map[string]any{}, nil
	}

	commandResponse := map[string]any{}
	err = common.UnmarshalJson([]byte(firstResponseData.(string)), &commandResponse)

	if err != nil {
		return map[string]any{}, err
	}

	return commandResponse, nil

}

// buildArgoCDFlags builds ArgoCD command flags based on configuration values
func buildArgoCDFlags(configValues []core.ToolConfigValue) string {
	var flags []string

	insecure := true
	grpcWeb := false
	timeout := "30"
	configFilePath := ""

	// Parse configuration values
	for _, cfg := range configValues {
		switch cfg.Name {
		case "insecure":
			if strings.ToLower(cfg.Value) == "true" {
				insecure = true
			}
		case "grpc_web":
			if strings.ToLower(cfg.Value) == "true" {
				grpcWeb = true
			}
		case "timeout":
			if cfg.Value != "" {
				timeout = cfg.Value
			}
		case "config_file_path":
			if cfg.Value != "" {
				configFilePath = cfg.Value
			}
		}
	}

	// Add server flag
	flags = append(flags, "--server $ARGOCD_SERVER")

	// Add authentication token (ArgoCD CLI only supports token auth via flags)
	flags = append(flags, "--auth-token $ARGOCD_AUTH_TOKEN")

	// Add optional flags
	if insecure {
		flags = append(flags, "--insecure")
	}

	if grpcWeb {
		flags = append(flags, "--grpc-web")
	}

	if timeout != "30" {
		flags = append(flags, "--request-timeout", timeout+"s")
	}

	if configFilePath != "" {
		flags = append(flags, "--config", configFilePath)
	}

	return strings.Join(flags, " ")
}

// isVMAgentMode checks if the integration config has connection_mode=vm_agent
func isVMAgentMode(values []core.ToolConfigValue) bool {
	for _, v := range values {
		if v.Name == "connection_mode" && v.Value == "vm_agent" {
			return true
		}
	}
	return false
}

// isDBProxyModule returns true for database modules that the proxy agent supports
func isDBProxyModule(module RelayJob) bool {
	return slices.Contains([]RelayJob{RelayJobPostgres, RelayJobMysql, RelayJobClickhouse, RelayJobRedis, RelayJobMssql, RelayJobOracle}, module)
}

// isSSHProxyModule returns true for SSH module that the proxy agent supports
func isSSHProxyModule(module RelayJob) bool {
	return module == RelayJobSSH
}

// oracleWorkspaceCommand holds the parsed components of a workspace sqlplus command.
type oracleWorkspaceCommand struct {
	SQL      string
	Database string
}

// extractOracleWorkspaceCommand parses a workspace sqlplus command.
// Expected format: sqlplus [-d "database"] -Q "SQL"
// Handles escaped quotes (\") inside the SQL string.
func extractOracleWorkspaceCommand(command string) oracleWorkspaceCommand {
	lower := strings.ToLower(command)
	if !strings.HasPrefix(lower, "sqlplus") {
		return oracleWorkspaceCommand{SQL: command}
	}

	result := oracleWorkspaceCommand{}

	// Extract -d flag for database override
	if dIdx := strings.Index(lower, " -d "); dIdx >= 0 {
		result.Database = extractEscapedQuotedArg(command[dIdx+4:])
	}

	// Extract -Q flag for SQL
	if qIdx := strings.Index(lower, " -q "); qIdx >= 0 {
		result.SQL = extractEscapedQuotedArg(command[qIdx+4:])
	} else {
		result.SQL = command
	}

	return result
}

// extractEscapedQuotedArg extracts a possibly-quoted argument, handling both
// double-quoted (`"arg"`, with `\"` escapes — the format tool_mssql.go etc.
// produce) and POSIX-shell single-quoted (`'arg'`, with `'\''` shell-escape —
// the format the workspace shim reserializes os.Args into before POSTing back
// to /api/v1/workspace/execute). Mirrors forager's sanitizeQuery extractor so
// behaviour is consistent on either side of the relay.
func extractEscapedQuotedArg(s string) string {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return ""
	}

	// Double-quoted: scan for closing ", honouring \" escapes.
	if s[0] == '"' {
		var result strings.Builder
		i := 1 // skip opening quote
		for i < len(s) {
			if s[i] == '\\' && i+1 < len(s) && s[i+1] == '"' {
				result.WriteByte('"')
				i += 2
				continue
			}
			if s[i] == '"' {
				return result.String()
			}
			result.WriteByte(s[i])
			i++
		}
		return result.String() // unclosed quote, return what we have
	}

	// Single-quoted: POSIX shells have no in-string escape for `'`, so the
	// standard trick is to close the quote, insert a literal `'` via `\'`,
	// and reopen — `'foo'\''bar'`. Reassemble by splitting on `'` and
	// collapsing any `'\''` sequence back into a single `'`.
	if s[0] == '\'' {
		inner := s[1:]
		var result strings.Builder
		for {
			end := strings.IndexByte(inner, '\'')
			if end < 0 {
				return result.String() // unclosed quote
			}
			result.WriteString(inner[:end])
			rest := inner[end+1:]
			if strings.HasPrefix(rest, `\''`) {
				result.WriteByte('\'')
				inner = rest[3:]
				continue
			}
			return result.String()
		}
	}

	// Unquoted: take the first space-delimited token.
	if idx := strings.IndexByte(s, ' '); idx >= 0 {
		return s[:idx]
	}
	return s
}

// unwrapCLIWrappedQuery strips the CLI-tool wrapping that the workspace shim
// adds (sqlcmd/psql/mariadb/sqlplus -Q/-c/-e "SQL") and returns the raw SQL
// plus any database flag found. Forager has equivalent logic in sanitizeQuery,
// but only on builds >= 2026-03-27 (commit 5754003). We unwrap here so the
// VM agent receives pure SQL regardless of forager version.
//
// Two guards against false positives:
//
//  1. We dispatch on `module` and only accept the wrapper prefix expected for
//     that module. A raw SQL query that happens to start with another tool's
//     name (e.g. a raw MSSQL query beginning with "psql") is returned
//     unchanged.
//
//  2. Database-flag lookups are bounded to the substring *before* the query
//     flag (`-Q` / `-c` / `-e`). The SQL payload lives after that flag, so
//     scanning the full string could match `-d` / `--dbname` tokens inside
//     string literals in the payload.
func unwrapCLIWrappedQuery(query string, module RelayJob) (sql string, database string) {
	q := strings.TrimSpace(query)
	lower := strings.ToLower(q)

	// flagArgBefore returns the value of `flag` if it appears before `limit`
	// in the lowered command line, otherwise "".
	flagArgBefore := func(flag string, limit int) string {
		head := lower
		if limit >= 0 && limit <= len(lower) {
			head = lower[:limit]
		}
		if idx := strings.Index(head, flag); idx >= 0 {
			return extractEscapedQuotedArg(q[idx+len(flag):])
		}
		return ""
	}

	switch module {
	case RelayJobMssql:
		// sqlcmd [-d "db"] -Q "SQL" -s "..." -W
		if !strings.HasPrefix(lower, "sqlcmd") {
			break
		}
		qIdx := strings.Index(lower, " -q ")
		if qIdx < 0 {
			break
		}
		return extractEscapedQuotedArg(q[qIdx+4:]), flagArgBefore(" -d ", qIdx)

	case RelayJobOracle:
		// sqlplus [-d "db"] -Q "SQL" (workspace convention; real sqlplus lacks -Q)
		if !strings.HasPrefix(lower, "sqlplus") {
			break
		}
		qIdx := strings.Index(lower, " -q ")
		if qIdx < 0 {
			break
		}
		return strings.TrimRight(extractEscapedQuotedArg(q[qIdx+4:]), "; \t\n"), flagArgBefore(" -d ", qIdx)

	case RelayJobPostgres:
		// psql [--dbname db] -c "\copy (SQL) TO stdout WITH CSV HEADER" | psql ... -c "SQL"
		if !strings.HasPrefix(lower, "psql") {
			break
		}
		cIdx := strings.Index(lower, " -c ")
		if cIdx < 0 {
			break
		}
		database = flagArgBefore(" --dbname ", cIdx)
		inner := extractEscapedQuotedArg(q[cIdx+4:])
		lowerInner := strings.ToLower(inner)
		if strings.HasPrefix(lowerInner, `\copy`) {
			open := strings.Index(inner, "(")
			closeMark := strings.LastIndex(strings.ToUpper(inner), ") TO")
			if open >= 0 && closeMark > open {
				return strings.TrimSpace(inner[open+1 : closeMark]), database
			}
		}
		return inner, database

	case RelayJobMysql:
		// mariadb [flags] -e "SQL" — no database flag extraction (connection uses $MYSQL_DATABASE env var).
		if !strings.HasPrefix(lower, "mariadb") && !strings.HasPrefix(lower, "mysql") {
			break
		}
		if idx := strings.Index(lower, " -e "); idx >= 0 {
			return extractEscapedQuotedArg(q[idx+4:]), ""
		}
	}
	// Wrong module prefix, unknown wrapper, or missing query flag — leave as-is.
	return query, ""
}

// getConfigValue returns the value of a named config from the tool config values.
func getConfigValue(values []core.ToolConfigValue, name string) string {
	for _, v := range values {
		if v.Name == name {
			return v.Value
		}
	}
	return ""
}

// executeViaProxyAgent sends a db_query request to the agent via the relay
func executeViaProxyAgent(toolContext core.NbToolContext, module RelayJob, query string, accountId string, configs map[string]any) (any, error) {
	// datasource_key is the agent-side datasource identifier — defaults to integration ID
	datasourceKey := getConfigValue(toolContext.ToolConfig.Values, "datasource_key")
	if datasourceKey == "" {
		if toolContext.ToolConfig.Id != "" {
			datasourceKey = toolContext.ToolConfig.Id
		} else {
			return nil, errors.New("vm_agent integration missing datasource_key config value")
		}
	}

	// agent_type is the relay agent type that handles this datasource (e.g. "k8s", "proxy")
	agentType := getConfigValue(toolContext.ToolConfig.Values, "agent_type")
	if agentType == "" {
		// Derive from connection_mode: vm_agent → proxy, otherwise k8s
		connectionMode := getConfigValue(toolContext.ToolConfig.Values, "connection_mode")
		if connectionMode == "vm_agent" {
			agentType = "proxy"
		} else {
			agentType = "k8s"
		}
	}

	params := map[string]any{
		"datasource_id": datasourceKey,
		"query":         query,
		"timeout_ms":    float64(config.Config.LlmServerRelayPodExecutionTimeoutSeconds * 1000),
	}
	if dbName, ok := configs["database"]; ok {
		if dbNameStr, ok := dbName.(string); ok && dbNameStr != "" {
			params["database"] = dbNameStr
		}
	}

	actionParam := relay.ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   "db_query",
		ActionParams: params,
		AgentType:    agentType,
		Timeout:      time.Second * time.Duration(config.Config.LlmServerRelayPodExecutionTimeoutSeconds),
	}

	response, err := relay.Execute(actionParam)
	if err != nil {
		return nil, fmt.Errorf("proxy agent db_query failed: %w", err)
	}

	return parseProxyDBResponse(response)
}

// parseProxyDBResponse converts the proxy agent's {columns, rows, row_count} response
// into an array-of-objects JSON string matching what callers expect.
func parseProxyDBResponse(response map[string]any) (string, error) {
	dataStr, ok := response["data"].(string)
	if !ok {
		return "", errors.New("proxy response missing 'data' field")
	}

	var dbResult map[string]any
	if err := json.Unmarshal([]byte(dataStr), &dbResult); err != nil {
		return "", fmt.Errorf("failed to parse proxy db_query data: %w", err)
	}

	// Check for error in response
	if errMsg, ok := dbResult["error"].(string); ok && errMsg != "" {
		return "", fmt.Errorf("proxy db_query error: %s", errMsg)
	}

	columnsRaw, ok := dbResult["columns"].([]any)
	if !ok && dbResult["columns"] != nil {
		return "", fmt.Errorf("proxy response 'columns' field has unexpected type %T", dbResult["columns"])
	}
	rowsRaw, ok2 := dbResult["rows"].([]any)
	if !ok2 && dbResult["rows"] != nil {
		return "", fmt.Errorf("proxy response 'rows' field has unexpected type %T", dbResult["rows"])
	}

	if columnsRaw == nil {
		return "[]", nil
	}

	// Extract column names from [{name: "col1", type: "text"}, ...]
	colNames := make([]string, len(columnsRaw))
	for i, c := range columnsRaw {
		if colMap, ok := c.(map[string]any); ok {
			if name, ok := colMap["name"].(string); ok {
				colNames[i] = name
			}
		}
	}

	// Convert rows [[val1, val2], ...] to [{col1: val1, col2: val2}, ...]
	result := make([]map[string]any, 0, len(rowsRaw))
	for _, row := range rowsRaw {
		rowArr, ok := row.([]any)
		if !ok {
			continue
		}
		obj := make(map[string]any, len(colNames))
		for i, colName := range colNames {
			if i < len(rowArr) {
				obj[colName] = rowArr[i]
			}
		}
		result = append(result, obj)
	}

	b, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// executeSSHViaProxyAgent sends an ssh_command request to the forager agent via the relay
func executeSSHViaProxyAgent(toolContext core.NbToolContext, command string, accountId string) (any, error) {
	datasourceKey := getConfigValue(toolContext.ToolConfig.Values, "datasource_key")
	if datasourceKey == "" {
		if toolContext.ToolConfig.Id != "" {
			datasourceKey = toolContext.ToolConfig.Id
		} else {
			return nil, errors.New("vm_agent integration missing datasource_key config value")
		}
	}

	agentType := getConfigValue(toolContext.ToolConfig.Values, "agent_type")
	if agentType == "" {
		connectionMode := getConfigValue(toolContext.ToolConfig.Values, "connection_mode")
		if connectionMode == "vm_agent" {
			agentType = "proxy"
		} else {
			agentType = "k8s"
		}
	}

	params := map[string]any{
		"datasource_id": datasourceKey,
		"command":       command,
		"timeout_ms":    float64(config.Config.LlmServerRelayPodExecutionTimeoutSeconds * 1000),
	}

	actionParam := relay.ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   "ssh_command",
		ActionParams: params,
		AgentType:    agentType,
		Timeout:      time.Second * time.Duration(config.Config.LlmServerRelayPodExecutionTimeoutSeconds),
	}

	response, err := relay.Execute(actionParam)
	if err != nil {
		return nil, fmt.Errorf("proxy agent ssh_command failed: %w", err)
	}

	return parseProxySSHResponse(response)
}

// parseProxySSHResponse extracts stdout/stderr from the proxy agent's SSH response.
func parseProxySSHResponse(response map[string]any) (string, error) {
	dataStr, ok := response["data"].(string)
	if !ok {
		return "", errors.New("proxy SSH response missing 'data' field")
	}

	var sshResult map[string]any
	if err := json.Unmarshal([]byte(dataStr), &sshResult); err != nil {
		return "", fmt.Errorf("failed to parse proxy ssh_command data: %w", err)
	}

	if errMsg, ok := sshResult["error"].(string); ok && errMsg != "" {
		return "", fmt.Errorf("proxy ssh_command error: %s", errMsg)
	}

	// Return as JSON with stdout/stderr fields
	result, err := json.Marshal(sshResult)
	if err != nil {
		return "", err
	}
	return string(result), nil
}

func ExecuteContainerJob(toolContext core.NbToolContext, module RelayJob, query string, accountId string, configs map[string]any, raw bool) (any, error) {

	if !slices.Contains([]RelayJob{RelayJobShell, RelayJobPostgres, RelayJobMysql, RelayJobMssql, RelayJobClickhouse, RelayJobOracle, RelayJobKubectl, RelayJobRabbitmq, RelayJobRedis, RelayJobHelm, RelayJobArgoCD, RelayJobSSH}, module) {
		return nil, errors.New("module not supported")
	}

	// Route DB queries to proxy agent for vm_agent integrations
	if isDBProxyModule(module) && isVMAgentMode(toolContext.ToolConfig.Values) {
		// When called from the workspace shim (raw=true) the query is already
		// wrapped in CLI-tool syntax (sqlcmd/psql/mariadb/sqlplus -Q/-c/-e "SQL").
		// Older forager builds don't strip this, so MSSQL/PG see the flags as
		// SQL tokens and fail with "Incorrect syntax near 'Q'/'d'". Unwrap here
		// to make the fix independent of forager binary version.
		if raw {
			unwrapped, db := unwrapCLIWrappedQuery(query, module)
			query = unwrapped
			if db != "" {
				if configs == nil {
					configs = map[string]any{}
				}
				configs["database"] = db
			}
		}
		return executeViaProxyAgent(toolContext, module, query, accountId, configs)
	}

	// Route SSH commands to proxy agent for vm_agent integrations
	if isSSHProxyModule(module) && isVMAgentMode(toolContext.ToolConfig.Values) {
		return executeSSHViaProxyAgent(toolContext, query, accountId)
	}

	explainQuery := false
	switch module {
	case RelayJobSSH:
		query = fmt.Sprintf(`mkdir -p ~/.ssh && echo "$SSH_KEY" > ~/.ssh/id_rsa && chmod 600 ~/.ssh/id_rsa && ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR $SSH_USER@$SSH_HOST "%s"`, query)
	case RelayJobPostgres:
		pgFlags := ""
		if dbName, ok := configs["database"]; ok {
			if dbNameStr, ok := dbName.(string); ok && dbNameStr != "" {
				pgFlags = " --dbname " + dbNameStr
			}
		}

		if !strings.HasPrefix(query, "psql") {
			if (strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "explain ")) || (strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "explain analyze")) {
				query = fmt.Sprintf(`psql %s -c "%s"`, pgFlags, query)
				explainQuery = true
			} else {
				query = strings.TrimSpace(query)
				query = strings.TrimSuffix(query, ";")
				query = fmt.Sprintf(`psql %s -c "\copy (%s) TO stdout WITH CSV HEADER"`, pgFlags, query)
			}
		} else {
			query = strings.Replace(query, "psql", "psql "+pgFlags, 1)
		}
	case RelayJobMysql:
		mysqlFlags := `--user=$MYSQL_USER --ssl=0 --password=$MYSQL_PASSWD --host=$MYSQL_HOST --port=$MYSQL_PORT`
		if dbName, ok := configs["database"]; ok {
			if dbNameStr, ok := dbName.(string); ok && dbNameStr != "" {
				mysqlFlags += " --database=" + dbNameStr
			}
		}

		if !raw {
			if (strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "explain ")) || (strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "explain analyze")) {
				query = fmt.Sprintf(`mariadb %s -e "%s"`, mysqlFlags, query)
				explainQuery = true
			} else {
				query = strings.TrimSpace(query)
				query = strings.TrimSuffix(query, ";")
				query = fmt.Sprintf(`mariadb %s -e "%s"`, mysqlFlags, query)
			}
		} else {
			if strings.Contains(query, "mysql") {
				query = strings.Replace(query, "mysql", "mysql "+mysqlFlags, 1)
			} else if strings.Contains(query, "mariadb") {
				query = strings.Replace(query, "mariadb", "mariadb "+mysqlFlags, 1)
			}
		}

	case RelayJobMssql:
		mssqlFlags := `-S "$MSSQL_HOST,$MSSQL_PORT" -U "$MSSQL_USER" -P "$MSSQL_PASSWORD"`
		if dbName, ok := configs["database"]; ok {
			if dbNameStr, ok := dbName.(string); ok && dbNameStr != "" {
				mssqlFlags += ` -d "` + dbNameStr + `"`
			}
		}

		if !raw {
			query = strings.TrimSpace(query)
			query = strings.TrimSuffix(query, ";")
			query = fmt.Sprintf(`sqlcmd %s -Q "%s" -s "	" -W`, mssqlFlags, query)
		} else {
			query = strings.Replace(query, "sqlcmd", "sqlcmd "+mssqlFlags, 1)
		}

	case RelayJobOracle:
		oracleService := "$ORACLE_SERVICE"
		if dbName, ok := configs["database"]; ok {
			if dbNameStr, ok := dbName.(string); ok && dbNameStr != "" {
				// Sanitize service name: only allow alphanumeric, dot, underscore, hyphen, dollar
				oracleService = regexp.MustCompile(`[^a-zA-Z0-9._\-$]`).ReplaceAllString(dbNameStr, "")
			}
		}

		if raw {
			// Workspace path: command is 'sqlplus [-d "db"] -Q "SQL"'.
			// Extract the SQL from the -Q flag and -d for database override.
			parsed := extractOracleWorkspaceCommand(query)
			query = parsed.SQL
			if parsed.Database != "" && oracleService == "$ORACLE_SERVICE" {
				oracleService = regexp.MustCompile(`[^a-zA-Z0-9._\-$]`).ReplaceAllString(parsed.Database, "")
			}
		}

		query = strings.TrimSpace(query)
		query = strings.TrimSuffix(query, ";")

		// Prevent heredoc breakout: remove any line that is exactly "EOF"
		lines := strings.Split(query, "\n")
		var safeLines []string
		for _, line := range lines {
			if strings.TrimSpace(line) != "EOF" {
				safeLines = append(safeLines, line)
			}
		}
		query = strings.Join(safeLines, "\n")

		query = fmt.Sprintf(
			`sqlplus -S $ORACLE_USER/$ORACLE_PASSWORD@//$ORACLE_HOST:${ORACLE_PORT:-1521}/%s <<'EOF'`+"\n"+
				`SET MARKUP CSV ON`+"\n"+
				`SET FEEDBACK OFF`+"\n"+
				`SET HEADING ON`+"\n"+
				`%s;`+"\n"+
				`EXIT;`+"\n"+
				`EOF`,
			oracleService, query,
		)

	case RelayJobClickhouse:
		chHost := "CLICKHOUSE_HOST"
		chPort := "9000"
		chDatabase := "default"
		chUserKeyInSecret := "CLICKHOUSE_USER"
		chPasswordKeyInSecret := "CLICKHOUSE_PASSWORD"
		chSecure := false

		for _, cfg := range toolContext.ToolConfig.Values {
			switch cfg.Name {
			case "host":
				chHost = cfg.Value
			case "port":
				if cfg.Value != "" {
					chPort = cfg.Value
				}
			case "database":
				if cfg.Value != "" {
					chDatabase = cfg.Value
				}
			case "secret_user_key":
				if cfg.Value != "" {
					chUserKeyInSecret = cfg.Value
				}
			case "secret_password_key":
				if cfg.Value != "" {
					chPasswordKeyInSecret = cfg.Value
				}
			case "secure_connection":
				if strings.ToLower(cfg.Value) == "true" {
					chSecure = true
					if chPort == "9000" && !configValueExists(toolContext.ToolConfig.Values, "port") {
						chPort = "9440"
					}
				}
			}
		}

		if dbName, ok := configs["database"]; ok {
			if dbNameStr, ok := dbName.(string); ok && dbNameStr != "" {
				chDatabase = dbName.(string)
			}
		}

		secureFlag := ""
		if chSecure {
			secureFlag = "--secure"
		}

		chFlags := fmt.Sprintf(`--host $%s --port %s --user $%s --password $%s --database %s %s`,
			chHost, chPort, chUserKeyInSecret, chPasswordKeyInSecret, chDatabase, secureFlag)

		if !raw {
			query = strings.TrimSpace(query)
			query = strings.TrimSuffix(query, ";")
			query = fmt.Sprintf(`clickhouse client %s --query "%s" --format CSVWithNames --send_logs_level=none --progress=0`,
				chFlags, query)
		} else {
			// For raw mode, try to inject flags if clickhouse client is present
			if strings.Contains(query, "clickhouse client") {
				query = strings.Replace(query, "clickhouse client", "clickhouse client "+chFlags, 1)
			} else if strings.Contains(query, "clickhouse-client") {
				query = strings.Replace(query, "clickhouse-client", "clickhouse-client "+chFlags, 1)
			}
		}

	case RelayJobRabbitmq:
		if strings.Contains(query, "rabbitmqadmin") {
			query = strings.Replace(query, "rabbitmqadmin", "rabbitmqadmin --host $RABBITMQ_HOST --port $RABBITMQ_PORT --username $RABBITMQ_USER --password $RABBITMQ_PASSWORD ", 1)
		} else if strings.Contains(query, "curl") && strings.Contains(query, "/api/") {
			// Inject credentials for RabbitMQ HTTP Management API calls.
			// Replace bare `curl` with `curl -s -u $RABBITMQ_USER:$RABBITMQ_PASSWORD` and
			// substitute the placeholder host/port so the agent can use $RABBITMQ_HOST and
			// $RABBITMQ_MGMT_PORT (defaults to 15672 if not set).
			query = strings.Replace(query, "curl ", "curl -s -u $RABBITMQ_USER:$RABBITMQ_PASSWORD ", 1)
			// Normalise any literal management-port placeholder the agent may emit.
			query = strings.ReplaceAll(query, "$RABBITMQ_PORT", "${RABBITMQ_MGMT_PORT:-15672}")
		}
	case RelayJobRedis:
		if strings.Contains(query, "redis-cli") {
			query = strings.Replace(query, "redis-cli", "redis-cli -h $REDIS_HOST --user $REDIS_USER --pass $REDIS_PASSWORD --no-auth-warning ", 1)
		} else {
			query = "redis-cli -h $REDIS_HOST --user $REDIS_USER --pass $REDIS_PASSWORD --no-auth-warning " + query
		}
	case RelayJobArgoCD:
		// ArgoCD commands need server URL and authentication
		if !strings.HasPrefix(query, "argocd") {
			query = "argocd " + query
		}

		// Build ArgoCD command with configuration options
		argoCDFlags := buildArgoCDFlags(toolContext.ToolConfig.Values)

		// Add server URL and authentication if provided via environment variables
		if strings.Contains(query, "argocd app") || strings.Contains(query, "argocd proj") || strings.Contains(query, "argocd cluster") || strings.Contains(query, "argocd repo") {
			query = strings.Replace(query, "argocd ", "argocd "+argoCDFlags+" ", 1)
		}
	}
	actionName := "pod_script_run_enricher"
	actionParams := map[string]any{
		"image":    config.Config.LlmServerShellImage,
		"command":  query,
		"pod_name": "nb-llm-" + uuid.NewString(),
	}
	if module == RelayJobClickhouse {
		envFromSecret := make(map[string]string)
		hostKey := "CLICKHOUSE_HOST"
		userKey := "CLICKHOUSE_USER"
		passKey := "CLICKHOUSE_PASSWORD"
		for _, cfg := range toolContext.ToolConfig.Values {
			if cfg.Name == "host_key_in_secret" && cfg.Value != "" {
				hostKey = cfg.Value
			}
			if cfg.Name == "user_key_in_secret" && cfg.Value != "" {
				userKey = cfg.Value
			}
			if cfg.Name == "password_key_in_secret" && cfg.Value != "" {
				passKey = cfg.Value
			}
		}
		envFromSecret[hostKey] = hostKey
		envFromSecret[userKey] = userKey
		envFromSecret[passKey] = passKey
		actionParams["env_from_secret_keys"] = envFromSecret
	}

	if module == RelayJobMssql {
		envFromSecret := map[string]string{
			"MSSQL_HOST":     "MSSQL_HOST",
			"MSSQL_PORT":     "MSSQL_PORT",
			"MSSQL_USER":     "MSSQL_USER",
			"MSSQL_PASSWORD": "MSSQL_PASSWORD",
			"MSSQL_DATABASE": "MSSQL_DATABASE",
		}
		actionParams["env_from_secret_keys"] = envFromSecret
	}

	if module == RelayJobOracle {
		envFromSecret := map[string]string{
			"ORACLE_HOST":     "ORACLE_HOST",
			"ORACLE_PORT":     "ORACLE_PORT",
			"ORACLE_USER":     "ORACLE_USER",
			"ORACLE_PASSWORD": "ORACLE_PASSWORD",
			"ORACLE_SERVICE":  "ORACLE_SERVICE",
		}
		actionParams["env_from_secret_keys"] = envFromSecret
	}

	if module == RelayJobArgoCD {
		envFromSecret := make(map[string]string)
		serverKey := "ARGOCD_SERVER"
		authTokenKey := "ARGOCD_AUTH_TOKEN"
		usernameKey := "ARGOCD_USERNAME"
		passwordKey := "ARGOCD_PASSWORD"
		authMethod := "token"

		for _, cfg := range toolContext.ToolConfig.Values {
			switch cfg.Name {
			case "server_key_in_secret":
				if cfg.Value != "" {
					serverKey = cfg.Value
				}
			case "auth_token_key_in_secret":
				if cfg.Value != "" {
					authTokenKey = cfg.Value
				}
			case "username_key_in_secret":
				if cfg.Value != "" {
					usernameKey = cfg.Value
				}
			case "password_key_in_secret":
				if cfg.Value != "" {
					passwordKey = cfg.Value
				}
			case "auth_method":
				if cfg.Value != "" {
					authMethod = cfg.Value
				}
			}
		}

		envFromSecret[serverKey] = serverKey

		if authMethod == "password" {
			envFromSecret[usernameKey] = usernameKey
			envFromSecret[passwordKey] = passwordKey
		} else {
			envFromSecret[authTokenKey] = authTokenKey
		}

		actionParams["env_from_secret_keys"] = envFromSecret
	}

	if module == RelayJobKubectl {
		actionName = "kubectl_command_executor"
		actionParams["command"] = query
	}

	actionParam := relay.ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   actionName,
		ActionParams: actionParams,
	}
	for _, v := range toolContext.ToolConfig.Values {
		if v.Name == "k8s_secret" {
			if strings.Contains(v.Value, "/") {
				namespaceAndSecret := strings.Split(v.Value, "/")
				actionParam.ActionParams["namespace"] = namespaceAndSecret[0]
				actionParam.ActionParams["secret"] = namespaceAndSecret[1]
			} else {
				actionParam.ActionParams["secret"] = v.Value
			}
			break
		}
	}
	actionParam.Timeout = time.Second * time.Duration(config.Config.LlmServerRelayPodExecutionTimeoutSeconds)
	response, err := relay.Execute(actionParam)
	if err != nil {
		return nil, err
	}

	responseParsed, err := getRelayCommandResponseData(response)
	if err != nil {
		return nil, err
	}
	if responseAny, ok := responseParsed["response"]; ok {
		responseStr, ok := responseAny.(string)
		if !ok {
			b, _ := json.Marshal(responseAny)
			responseStr = string(b)
		}
		if raw {
			// If it's a JSON string (typical for relay executors), try to extract stdout/stderr
			trimmed := strings.TrimSpace(responseStr)
			if strings.HasPrefix(trimmed, "{") {
				var execResult map[string]any
				if err := json.Unmarshal([]byte(trimmed), &execResult); err == nil {
					stdout, hasStdout := execResult["stdout"].(string)
					stderr, hasStderr := execResult["stderr"].(string)
					if hasStdout || hasStderr {
						output := stdout
						if stderr != "" {
							if output != "" && !strings.HasSuffix(output, "\n") {
								output += "\n"
							}
							output += stderr
						}
						return output, nil
					}
				}
			}
			return responseStr, nil
		}
		switch module {
		case RelayJobPostgres:
			if explainQuery {
				return fmt.Sprintf(`[{"plan": %s}]`, responseStr), nil
			} else {
				return convertCsvToJsonString(toolContext, responseStr, rune(',')), nil
			}
		case RelayJobMysql:
			if explainQuery {
				return fmt.Sprintf(`[{"plan": %s}]`, responseStr), nil
			} else {
				return convertCsvToJsonString(toolContext, responseStr, rune('\t')), nil
			}
		case RelayJobMssql:
			return convertCsvToJsonString(toolContext, responseStr, rune('\t')), nil
		case RelayJobOracle:
			return convertCsvToJsonString(toolContext, responseStr, rune(',')), nil
		case RelayJobClickhouse:
			// some times response startswith -- "Decompressing the binary"
			// in those cases we need to remove that part
			// remove first line if it starts with "Decompressing the binary..."
			if strings.HasPrefix(responseStr, "Decompressing the binary") {
				lines := strings.Split(responseStr, "\n")
				if len(lines) > 1 {
					responseStr = strings.Join(lines[1:], "\n")
				}
			}
			return convertCsvToJsonString(toolContext, responseStr, rune(',')), nil
		default:
			outputformat := map[string]string{
				"stdout": responseStr,
			}
			outputformatBytes, err := common.MarshalJson(outputformat)
			if err != nil {
				return nil, err
			}
			return string(outputformatBytes), nil
		}
	}
	responseParsedbytes, err := common.MarshalJson(responseParsed)
	if err != nil {
		toolContext.Ctx.GetLogger().Error("unable to execute command", "query", query, "response", responseParsed, "error", err)
		return nil, err
	}
	return string(responseParsedbytes), nil
}
