package relay

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/runbook/common"
	"nudgebee/runbook/config"
	"nudgebee/runbook/services/integrations"
	"nudgebee/runbook/services/security"
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
	RelayJobPostgres   RelayJob = "postgresql"
	RelayJobMysql      RelayJob = "mysql"
	RelayJobRabbitmq   RelayJob = "rabbitmq"
	RelayJobRedis      RelayJob = "redis"
	RelayJobClickhouse RelayJob = "clickhouse"
	RelayJobMssql      RelayJob = "mssql"
	RelayJobOracle     RelayJob = "oracle"
	RelayJobSSH        RelayJob = "ssh"
)

func ExecuteRelayJob(requestContext *security.RequestContext, accountId string, module RelayJob, integrationId string, query string, configs map[string]any) (any, error) {

	if !slices.Contains([]RelayJob{RelayJobShell, RelayJobPostgres, RelayJobMysql, RelayJobMssql, RelayJobOracle, RelayJobClickhouse, RelayJobKubectl, RelayJobRabbitmq, RelayJobRedis, RelayJobHelm, RelayJobArgoCD, RelayJobSSH}, module) {
		return nil, errors.New("module not supported")
	}

	var integrationConfig integrations.IntegrationConfig
	if integrationId == "" && module != RelayJobKubectl && module != RelayJobShell {
		return nil, errors.New("integrationId required")
	} else if integrationId != "" {
		var err error
		integrationConfig, err = integrations.GetIntegration(requestContext, accountId, string(module), integrationId)
		if err != nil {
			return nil, err
		}
	}
	integrationConfigs := integrationConfig.Values

	// Route DB queries to proxy agent for vm_agent integrations
	if isDBProxyModule(module) && isVMAgentModeIntegration(integrationConfigs) {
		return executeViaProxyAgentRunbook(integrationConfig, module, query, accountId, configs)
	}

	// Route SSH commands to proxy agent for vm_agent integrations
	if module == RelayJobSSH && isVMAgentModeIntegration(integrationConfigs) {
		return executeSSHViaProxyAgentRunbook(integrationConfig, query, accountId)
	}

	explainQuery := false
	switch module {
	case RelayJobSSH:
		query = fmt.Sprintf(`mkdir -p ~/.ssh && echo "$SSH_KEY" > ~/.ssh/id_rsa && chmod 600 ~/.ssh/id_rsa && ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR $SSH_USER@$SSH_HOST '%s'`, escapeSingleQuotes(query))
	case RelayJobPostgres:
		if (strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "explain ")) || (strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "explain analyze")) {
			query = fmt.Sprintf(`psql -c '%s'`, escapeSingleQuotes(query))
			explainQuery = true
		} else {
			query = strings.TrimSpace(query)
			query = strings.TrimSuffix(query, ";")
			query = fmt.Sprintf(`psql -c '\copy (%s) TO stdout WITH CSV HEADER'`, escapeSingleQuotes(query))
		}
		if dbName, ok := configs["database"]; ok {
			if dbNameStr, ok := dbName.(string); ok && dbNameStr != "" {
				query = query + " --dbname " + dbName.(string)
			}
		}
	case RelayJobMysql:
		if (strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "explain ")) || (strings.HasPrefix(strings.ToLower(strings.TrimSpace(query)), "explain analyze")) {
			query = fmt.Sprintf(`mariadb --user $MYSQL_USER --ssl=0 --password=$MYSQL_PASSWD --host $MYSQL_HOST --port $MYSQL_PORT --database $MYSQL_DATABASE -e '%s'`, escapeSingleQuotes(query))
			explainQuery = true
		} else {
			query = strings.TrimSpace(query)
			query = strings.TrimSuffix(query, ";")
			query = fmt.Sprintf(`mariadb --user $MYSQL_USER --ssl=0 --password=$MYSQL_PASSWD --host $MYSQL_HOST --port $MYSQL_PORT --database $MYSQL_DATABASE -e '%s'`, escapeSingleQuotes(query))
		}

		if dbName, ok := configs["database"]; ok {
			if dbNameStr, ok := dbName.(string); ok && dbNameStr != "" {
				query = strings.Replace(query, "$MYSQL_DATABASE", dbName.(string), 1)
			}
		}

	case RelayJobMssql:
		mssqlFlags := `-S "$MSSQL_HOST,$MSSQL_PORT" -U "$MSSQL_USER" -P "$MSSQL_PASSWORD"`
		if dbName, ok := configs["database"]; ok {
			if dbNameStr, ok := dbName.(string); ok && dbNameStr != "" {
				mssqlFlags += fmt.Sprintf(` -d '%s'`, escapeSingleQuotes(dbNameStr))
			}
		}
		query = strings.TrimSpace(query)
		query = strings.TrimSuffix(query, ";")
		query = fmt.Sprintf(`sqlcmd %s -Q '%s' -s $'\t' -W`, mssqlFlags, escapeSingleQuotes(query))

	case RelayJobOracle:
		oracleService := "$ORACLE_SERVICE"
		if dbName, ok := configs["database"]; ok {
			if dbNameStr, ok := dbName.(string); ok && dbNameStr != "" {
				oracleService = oracleServiceNameSanitizer.ReplaceAllString(dbNameStr, "")
			}
		}
		query = strings.TrimSpace(query)
		query = strings.TrimSuffix(query, ";")
		// Remove any line that is exactly "EOF" to prevent heredoc breakout
		lines := strings.Split(query, "\n")
		var safeLines []string
		for _, line := range lines {
			if strings.TrimSpace(line) != "EOF" {
				safeLines = append(safeLines, line)
			}
		}
		safeQuery := strings.Join(safeLines, "\n")
		query = fmt.Sprintf(`sqlplus -S $ORACLE_USER/$ORACLE_PASSWORD@//$ORACLE_HOST:${ORACLE_PORT:-1521}/%s <<'EOF'
SET PAGESIZE 0 FEEDBACK OFF HEADING ON LINESIZE 32767 COLSEP ','
%s;
EXIT;
EOF`, oracleService, safeQuery)

	case RelayJobClickhouse:
		chHost := "CLICKHOUSE_HOST"
		chPort := "9000"
		chDatabase := "default"
		chUserKeyInSecret := "CLICKHOUSE_USER"
		chPasswordKeyInSecret := "CLICKHOUSE_PASSWORD"
		chSecure := false

		for _, cfg := range integrationConfigs {
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
					if chPort == "9000" && !configValueExists(integrationConfigs, "port") {
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

		query = strings.TrimSpace(query)
		query = strings.TrimSuffix(query, ";")
		query = fmt.Sprintf(`clickhouse client --host $%s --port %s --user $%s --password $%s --database %s %s --query '%s' --format CSVWithNames --send_logs_level=none --progress=0`,
			chHost, chPort, chUserKeyInSecret, chPasswordKeyInSecret, chDatabase, secureFlag, escapeSingleQuotes(query))

	case RelayJobRabbitmq:
		query = strings.Replace(query, "rabbitmqadmin", "rabbitmqadmin --host $RABBITMQ_HOST --port $RABBITMQ_PORT --username $RABBITMQ_USER --password $RABBITMQ_PASSWORD ", 1)
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
		argoCDFlags := buildArgoCDFlags(integrationConfigs)

		// Add server URL and authentication if provided via environment variables
		if strings.Contains(query, "argocd app") || strings.Contains(query, "argocd proj") || strings.Contains(query, "argocd cluster") || strings.Contains(query, "argocd repo") {
			query = strings.Replace(query, "argocd ", "argocd "+argoCDFlags+" ", 1)
		}
	}
	actionName := "pod_script_run_enricher"
	// Default timeout from global config; callers may override via configs["timeout_ms"]
	// (e.g. AgentExecutor passes the remaining task-level Temporal deadline).
	timeoutMs := float64(config.Config.RunbookServerRelayPodExecutionTimeoutSeconds * 1000)
	// Ceiling division: ensures e.g. 90s → 2min rather than 1min (floor).
	waitThreshold := (config.Config.RunbookServerRelayPodExecutionTimeoutSeconds + 59) / 60
	if waitThreshold < 1 {
		waitThreshold = 1
	}
	actionParams := map[string]any{
		"image":          config.Config.LlmServerShellImage,
		"command":        query,
		"pod_name":       "nb-runbook-" + uuid.NewString(),
		"wait_threshold": waitThreshold,
	}
	// Allow callers (e.g. AgentExecutor) to override the pod wait via configs["timeout_ms"].
	// Applied globally before the switch so it takes effect for all relay modules that use
	// pod_script_run_enricher (not just RelayJobShell).
	if callerTimeoutMs, ok := configs["timeout_ms"].(float64); ok && callerTimeoutMs > 0 {
		timeoutMs = callerTimeoutMs
		d := time.Duration(callerTimeoutMs) * time.Millisecond
		actionParams["wait_threshold"] = int((d + time.Minute - 1) / time.Minute)
	}
	switch module {
	case RelayJobClickhouse:
		envFromSecret := make(map[string]string)
		hostKey := "CLICKHOUSE_HOST"
		userKey := "CLICKHOUSE_USER"
		passKey := "CLICKHOUSE_PASSWORD"
		for _, cfg := range integrationConfigs {
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
	case RelayJobMssql:
		actionParams["env_from_secret_keys"] = map[string]string{
			"MSSQL_HOST":     "MSSQL_HOST",
			"MSSQL_PORT":     "MSSQL_PORT",
			"MSSQL_USER":     "MSSQL_USER",
			"MSSQL_PASSWORD": "MSSQL_PASSWORD",
			"MSSQL_DATABASE": "MSSQL_DATABASE",
		}
	case RelayJobOracle:
		actionParams["env_from_secret_keys"] = map[string]string{
			"ORACLE_HOST":     "ORACLE_HOST",
			"ORACLE_PORT":     "ORACLE_PORT",
			"ORACLE_USER":     "ORACLE_USER",
			"ORACLE_PASSWORD": "ORACLE_PASSWORD",
			"ORACLE_SERVICE":  "ORACLE_SERVICE",
		}
	case RelayJobArgoCD:
		envFromSecret := make(map[string]string)
		serverKey := "ARGOCD_SERVER"
		authTokenKey := "ARGOCD_AUTH_TOKEN"
		usernameKey := "ARGOCD_USERNAME"
		passwordKey := "ARGOCD_PASSWORD"
		authMethod := "password"

		for _, cfg := range integrationConfigs {
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
	case RelayJobKubectl:
		actionName = "kubectl_command_executor"
		actionParams["command"] = query
	case RelayJobShell:
		actionParams["command"] = query
		if dockerImage, ok := configs["image"].(string); ok && dockerImage != "" {
			actionParams["image"] = dockerImage
		}
		if k8sNamespace, ok := configs["namespace"].(string); ok && k8sNamespace != "" {
			actionParams["namespace"] = k8sNamespace
		}
	}

	actionParam := ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   actionName,
		ActionParams: actionParams,
	}
	for _, v := range integrationConfigs {
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
	actionParam.Timeout = time.Duration(timeoutMs) * time.Millisecond
	response, err := ExecuteRelay(actionParam)
	if err != nil {
		return nil, err
	}

	responseParsed, err := getRelayCommandResponseData(response)
	if err != nil {
		return nil, err
	}
	if responseAny, ok := responseParsed["response"]; ok {
		switch module {
		case RelayJobPostgres:
			raw := responseAny.(string)
			if err := detectDBQueryError(RelayJobPostgres, raw); err != nil {
				requestContext.GetLogger().Error("Postgres query failed", "error", err, "raw", raw)
				return nil, err
			}
			if explainQuery {
				return fmt.Sprintf(`[{"plan": %s}]`, raw), nil
			}
			return convertCsvToJsonString(requestContext, raw, rune(',')), nil
		case RelayJobMysql:
			raw := responseAny.(string)
			if err := detectDBQueryError(RelayJobMysql, raw); err != nil {
				requestContext.GetLogger().Error("MySQL query failed", "error", err, "raw", raw)
				return nil, err
			}
			if explainQuery {
				return fmt.Sprintf(`[{"plan": %s}]`, raw), nil
			}
			return convertCsvToJsonString(requestContext, raw, rune('\t')), nil
		case RelayJobMssql:
			rawResponse, ok := responseAny.(string)
			if !ok {
				return nil, errors.New("mssql relay response is not a string")
			}
			if err := detectSqlcmdError(rawResponse); err != nil {
				requestContext.GetLogger().Error("MSSQL query failed", "error", err, "raw", rawResponse)
				return nil, err
			}
			cleaned := cleanSqlcmdOutput(rawResponse)
			return convertCsvToJsonString(requestContext, cleaned, rune('\t')), nil
		case RelayJobOracle:
			raw := responseAny.(string)
			if err := detectDBQueryError(RelayJobOracle, raw); err != nil {
				requestContext.GetLogger().Error("Oracle query failed", "error", err, "raw", raw)
				return nil, err
			}
			return convertCsvToJsonString(requestContext, raw, rune(',')), nil
		case RelayJobClickhouse:
			raw := responseAny.(string)
			if err := detectDBQueryError(RelayJobClickhouse, raw); err != nil {
				requestContext.GetLogger().Error("ClickHouse query failed", "error", err, "raw", raw)
				return nil, err
			}
			// some times response startswith -- "Decompressing the binary"
			// in those cases we need to remove that part
			if strings.HasPrefix(raw, "Decompressing the binary") {
				lines := strings.Split(raw, "\n")
				if len(lines) > 1 {
					raw = strings.Join(lines[1:], "\n")
				}
			}
			return convertCsvToJsonString(requestContext, raw, rune(',')), nil
		default:
			outputformat := map[string]string{
				"stdout": responseAny.(string),
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
		requestContext.GetLogger().Error("unable to execute command", "query", query, "response", responseParsed, "error", err)
		return nil, err
	}
	return string(responseParsedbytes), nil
}

func getRelayCommandResponseData(relayResponse map[string]any) (map[string]any, error) {
	data1, ok := relayResponse["data"].(map[string]any)
	if !ok || data1 == nil {
		return nil, errors.New("data1 field not found or is nil from response")
	}

	// Surface relay-side errors. The relay signals failures via success=false
	// and/or a non-2xx status_code along with a human-readable msg (e.g.
	// ImagePullBackOff, timeout, pod eviction). Without this check those
	// failures get silently swallowed and callers see empty results.
	if success, ok := data1["success"].(bool); ok && !success {
		msg, _ := data1["msg"].(string)
		if msg == "" {
			msg = "relay reported failure"
		}
		return nil, fmt.Errorf("relay execution failed: %s", msg)
	}
	if statusCode, ok := data1["status_code"].(float64); ok && statusCode >= 400 {
		msg, _ := data1["msg"].(string)
		if msg == "" {
			msg = fmt.Sprintf("relay returned status %d", int(statusCode))
		}
		return nil, fmt.Errorf("relay execution failed: %s", msg)
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
func buildArgoCDFlags(configValues []integrations.IntegrationConfigValue) string {
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

// cleanSqlcmdOutput strips the dashes separator line (always line 2 in sqlcmd -s"\t" output)
// and trailing "(N rows affected)" / empty lines before CSV parsing.
func cleanSqlcmdOutput(output string) string {
	lines := strings.Split(output, "\n")
	var cleaned []string
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		isDashLine := trimmed != "" && strings.TrimLeft(trimmed, "- \t") == ""
		if (i == 1 && isDashLine) || trimmed == "" || (strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, "affected)")) {
			continue
		}
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

// detectSqlcmdError inspects raw sqlcmd output for shell or sqlcmd errors that
// would otherwise be silently parsed as empty CSV results.
func detectSqlcmdError(output string) error {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}
	// Shell errors: explicit shell "not found" / "permission denied" formats.
	firstLine := strings.TrimSpace(strings.SplitN(trimmed, "\n", 2)[0])
	lowerFirst := strings.ToLower(firstLine)
	if strings.Contains(lowerFirst, ": not found") ||
		strings.Contains(lowerFirst, ": command not found") ||
		strings.Contains(lowerFirst, "permission denied") ||
		strings.Contains(lowerFirst, "no such file or directory") {
		return fmt.Errorf("sqlcmd execution failed: %s", firstLine)
	}
	// sqlcmd CLI errors: "Sqlcmd: Error: ..." / "Sqlcmd: ..."
	// SQL Server engine errors: "Msg NNNN, Level N, State N, ..."
	if strings.HasPrefix(lowerFirst, "sqlcmd: ") || strings.HasPrefix(lowerFirst, "msg ") {
		return fmt.Errorf("sqlcmd error: %s", firstLine)
	}
	return nil
}

// detectDBQueryError checks raw DB client output for known error prefixes before
// it reaches the CSV parser. Without this, a query error like "ERROR: relation
// not found" gets parsed as a single-row CSV with zero data rows and returns [].
//
// Each DB client has its own error format:
//   - PostgreSQL: psql outputs "ERROR:", "FATAL:", or "psql:" prefixed lines
//   - MySQL:      mysql outputs "ERROR NNNN (XXXXX): ..." lines
//   - ClickHouse: clickhouse-client outputs "Code: N. DB::Exception: ..."
//   - Oracle:     sqlplus outputs "ORA-NNNNN:", "SP2-NNNN:", or "PLS-NNNNN:" lines
func detectDBQueryError(module RelayJob, output string) error {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}
	firstLine := strings.TrimSpace(strings.SplitN(trimmed, "\n", 2)[0])
	lower := strings.ToLower(firstLine)

	switch module {
	case RelayJobPostgres:
		if strings.HasPrefix(lower, "error:") || strings.HasPrefix(lower, "fatal:") || strings.HasPrefix(lower, "psql:") {
			return fmt.Errorf("postgres error: %s", firstLine)
		}
	case RelayJobMysql:
		if strings.HasPrefix(lower, "error ") {
			return fmt.Errorf("mysql error: %s", firstLine)
		}
	case RelayJobClickhouse:
		if strings.HasPrefix(lower, "code:") || strings.Contains(lower, "db::exception") {
			return fmt.Errorf("clickhouse error: %s", firstLine)
		}
	case RelayJobOracle:
		if strings.HasPrefix(lower, "ora-") || strings.HasPrefix(lower, "sp2-") || strings.HasPrefix(lower, "pls-") {
			return fmt.Errorf("oracle error: %s", firstLine)
		}
	}
	return nil
}

func convertCsvToJsonString(toolContext *security.RequestContext, csvData string, seprator rune) string {
	reader := csv.NewReader(strings.NewReader(csvData))
	reader.Comma = rune(seprator)
	reader.FieldsPerRecord = -1 // allow variable number of fields per row
	records, err := reader.ReadAll()

	if err != nil {
		slog.Warn("Unable to parse CSV:", "error", err)
		return csvData
	}

	// Remove BOM character if present
	if len(records) > 0 && len(records[0]) > 0 {
		records[0][0] = strings.TrimPrefix(records[0][0], "\ufeff")
	}

	if len(records) < 2 {
		return "[]"
	}

	headers := records[0]
	var jsonArray []map[string]string

	for _, row := range records[1:] {
		if len(row) != len(headers) {
			toolContext.GetLogger().Error("Row length mismatch:", "row", slog.AnyValue(row))
			continue
		}
		rowMap := make(map[string]string)
		for i, value := range row {
			rowMap[headers[i]] = value
		}
		jsonArray = append(jsonArray, rowMap)
	}

	jsonData, err := common.MarshalJson(jsonArray)
	if err != nil {
		toolContext.GetLogger().Error("Error marshaling JSON:", "error", err)
		return csvData
	}

	return string(jsonData)
}

// isVMAgentModeIntegration checks if the integration config has connection_mode=vm_agent
func isVMAgentModeIntegration(values []integrations.IntegrationConfigValue) bool {
	for _, v := range values {
		if v.Name == "connection_mode" && v.Value == "vm_agent" {
			return true
		}
	}
	return false
}

// isDBProxyModule returns true for database modules that the proxy agent supports
func isDBProxyModule(module RelayJob) bool {
	return slices.Contains([]RelayJob{RelayJobPostgres, RelayJobMysql, RelayJobMssql, RelayJobOracle, RelayJobClickhouse, RelayJobRedis}, module)
}

// getIntegrationConfigValue returns the value of a named config from integration config values.
func getIntegrationConfigValue(values []integrations.IntegrationConfigValue, name string) string {
	for _, v := range values {
		if v.Name == name {
			return v.Value
		}
	}
	return ""
}

// executeViaProxyAgentRunbook sends a db_query request to the agent via the relay
func executeViaProxyAgentRunbook(integration integrations.IntegrationConfig, module RelayJob, query string, accountId string, configs map[string]any) (any, error) {
	// datasource_key is the agent-side datasource identifier — defaults to integration ID
	datasourceKey := getIntegrationConfigValue(integration.Values, "datasource_key")
	if datasourceKey == "" {
		if integration.Id != "" {
			datasourceKey = integration.Id
		} else {
			return nil, errors.New("vm_agent integration missing datasource_key config value")
		}
	}

	// agent_type is the relay agent type that handles this datasource (e.g. "k8s", "proxy")
	agentType := getIntegrationConfigValue(integration.Values, "agent_type")
	if agentType == "" {
		// Derive from connection_mode: vm_agent → proxy, otherwise k8s
		connectionMode := getIntegrationConfigValue(integration.Values, "connection_mode")
		if connectionMode == "vm_agent" {
			agentType = "proxy"
		} else {
			agentType = "k8s"
		}
	}

	params := map[string]any{
		"datasource_id": datasourceKey,
		"query":         query,
		"timeout_ms":    float64(config.Config.RunbookServerRelayPodExecutionTimeoutSeconds * 1000),
	}
	if dbName, ok := configs["database"]; ok {
		if dbNameStr, ok := dbName.(string); ok && dbNameStr != "" {
			params["database"] = dbNameStr
		}
	}

	actionParam := ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   "db_query",
		ActionParams: params,
		AgentType:    agentType,
		Timeout:      time.Second * time.Duration(config.Config.RunbookServerRelayPodExecutionTimeoutSeconds),
	}

	response, err := ExecuteRelay(actionParam)
	if err != nil {
		return nil, fmt.Errorf("proxy agent db_query failed: %w", err)
	}

	return parseProxyDBResponseRunbook(response)
}

// parseProxyDBResponseRunbook converts the proxy agent's {columns, rows, row_count} response
// into an array-of-objects JSON string matching what callers expect.
func parseProxyDBResponseRunbook(response map[string]any) (string, error) {
	dataStr, ok := response["data"].(string)
	if !ok {
		return "", errors.New("proxy response missing 'data' field")
	}

	var dbResult map[string]any
	if err := json.Unmarshal([]byte(dataStr), &dbResult); err != nil {
		return "", fmt.Errorf("failed to parse proxy db_query data: %w", err)
	}

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

	colNames := make([]string, len(columnsRaw))
	for i, c := range columnsRaw {
		if colMap, ok := c.(map[string]any); ok {
			if name, ok := colMap["name"].(string); ok {
				colNames[i] = name
			}
		}
	}

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

func configValueExists(configs []integrations.IntegrationConfigValue, name string) bool {
	for _, cfg := range configs {
		if cfg.Name == name && cfg.Value != "" {
			return true
		}
	}
	return false
}

func escapeSingleQuotes(str string) string {
	return strings.ReplaceAll(str, "'", `'\''`)
}

// oracleServiceNameSanitizer strips characters unsafe for shell interpolation in Oracle service names.
var oracleServiceNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9._\-$]`)

// executeSSHViaProxyAgentRunbook sends an ssh_command request to the forager agent via the relay.
func executeSSHViaProxyAgentRunbook(integration integrations.IntegrationConfig, command string, accountId string) (any, error) {
	datasourceKey := getIntegrationConfigValue(integration.Values, "datasource_key")
	if datasourceKey == "" {
		if integration.Id != "" {
			datasourceKey = integration.Id
		} else {
			return nil, errors.New("vm_agent integration missing datasource_key config value")
		}
	}

	agentType := getIntegrationConfigValue(integration.Values, "agent_type")
	if agentType == "" {
		connectionMode := getIntegrationConfigValue(integration.Values, "connection_mode")
		if connectionMode == "vm_agent" {
			agentType = "proxy"
		} else {
			agentType = "k8s"
		}
	}

	params := map[string]any{
		"datasource_id": datasourceKey,
		"command":       command,
		"timeout_ms":    float64(config.Config.RunbookServerRelayPodExecutionTimeoutSeconds * 1000),
	}

	actionParam := ActionExecuteBody{
		AccountID:    accountId,
		ActionName:   "ssh_command",
		ActionParams: params,
		AgentType:    agentType,
		Timeout:      time.Second * time.Duration(config.Config.RunbookServerRelayPodExecutionTimeoutSeconds),
	}

	response, err := ExecuteRelay(actionParam)
	if err != nil {
		return nil, fmt.Errorf("proxy agent ssh_command failed: %w", err)
	}

	return parseProxySSHResponseRunbook(response)
}

// parseProxySSHResponseRunbook extracts stdout/stderr from the proxy agent's SSH response.
func parseProxySSHResponseRunbook(response map[string]any) (string, error) {
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

	result, err := json.Marshal(sshResult)
	if err != nil {
		return "", err
	}
	return string(result), nil
}
