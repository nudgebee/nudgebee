// pkg/server/handlers/workspace.go
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/db"
	"nudgebee/relay-server/pkg/mq"
	"nudgebee/relay-server/pkg/utils"
)

// sanitizeNameRe strips characters that are unsafe for use in shell command arguments
// (e.g. database names, service names). Only alphanumeric, dot, underscore, hyphen, and $ are kept.
var sanitizeNameRe = regexp.MustCompile(`[^a-zA-Z0-9._\-$]`)

// workspaceTokenClaims are the JWT claims embedded in workspace tokens issued by llm-server.
type workspaceTokenClaims struct {
	AccountId string `json:"account_id"`
	TenantId  string `json:"tenant_id"`
	jwt.RegisteredClaims
}

// workspaceExecuteRequest mirrors the shim payload.
type workspaceExecuteRequest struct {
	AccountId  string         `json:"account_id"`
	Tool       string         `json:"tool"`
	Command    string         `json:"command"`
	Arguments  map[string]any `json:"arguments"`
	ConfigName string         `json:"config_name"`
}

// NewWorkspaceExecuteHandler handles POST /workspace/execute from workspace pod shims.
// It validates the workspace JWT token, resolves integration config from the DB,
// builds the relay action, and dispatches via RabbitMQ to the k8s agent.
func NewWorkspaceExecuteHandler(
	store db.AgentStore,
	rpcClient mq.RPCClient,
	cfg *config.Config,
	rootTracer *trace.Tracer,
	rootMeter *metric.Meter,
	rootLogger *slog.Logger,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Parse and validate request body
		var req workspaceExecuteRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "invalid request body"))
			return
		}
		if req.AccountId == "" || req.Tool == "" {
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "account_id and tool are required"))
			return
		}
		if req.Command == "" {
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "command is required"))
			return
		}
		// Reject commands that are clearly oversized (>16KB) to limit blast radius of injection attempts.
		// The JWT token authenticates the workspace pod, but belt-and-suspenders validation reduces risk.
		if len(req.Command) > 16*1024 {
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "command exceeds maximum allowed length"))
			return
		}

		// 2. Validate workspace JWT
		tokenString := c.GetHeader("X-Workspace-Token")
		if tokenString == "" {
			c.JSON(http.StatusUnauthorized, utils.BuildError(401, "missing X-Workspace-Token"))
			return
		}
		claims, err := parseWorkspaceToken(tokenString, cfg.Security.WorkspaceJWTSecret)
		if err != nil {
			rootLogger.Error("workspace: invalid token", "err", err, "account_id", req.AccountId)
			c.JSON(http.StatusUnauthorized, utils.BuildError(401, "invalid or expired token"))
			return
		}
		if claims.AccountId != req.AccountId {
			c.JSON(http.StatusUnauthorized, utils.BuildError(401, "token account mismatch"))
			return
		}

		logger, _, _ := utils.BuildContextFromPayload(c, rootTracer, rootMeter, rootLogger)
		logger.Info("workspace: executing tool", "tool", req.Tool, "account_id", req.AccountId)

		// 3a. Fast-fail if agent is not connected (avoids 180s RPC timeout)
		connected, err := store.IsAgentConnected(c.Request.Context(), req.AccountId, "k8s")
		if err != nil {
			logger.Error("workspace: failed to check agent connectivity", "account_id", req.AccountId, "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "internal error"))
			return
		}
		if !connected {
			logger.Warn("workspace: agent not connected", "account_id", req.AccountId)
			c.JSON(http.StatusBadRequest, utils.BuildError(400, "agent not connected"))
			return
		}

		// 3. Resolve integration config (tools that need a k8s secret)
		toolName := strings.ToLower(req.Tool)
		integrationType := workspaceToolToIntegrationType(toolName)
		var configValues []db.WorkspaceConfigValue
		if integrationType != "" {
			configValues, err = store.GetWorkspaceToolConfig(c.Request.Context(), req.AccountId, integrationType, req.ConfigName)
			if err != nil {
				logger.Error("workspace: failed to get tool config", "tool", toolName, "integration_type", integrationType, "err", err)
				c.JSON(http.StatusInternalServerError, utils.BuildError(500, "failed to load tool configuration"))
				return
			}
			if len(configValues) == 0 {
				logger.Warn("workspace: no integration config found for tool", "tool", toolName, "config_name", req.ConfigName)
			}
		}

		// 4. Build action name and params
		actionName, actionParams, err := buildWorkspaceAction(toolName, req.Command, configValues, req.Arguments, cfg.Workspace.ShellImage)
		if err != nil {
			logger.Error("workspace: unsupported tool", "tool", req.Tool)
			c.JSON(http.StatusBadRequest, utils.BuildError(400, fmt.Sprintf("unsupported tool: %s", req.Tool)))
			return
		}

		// 5. Build relay request envelope (same structure as /request handler)
		reqID := uuid.NewString()
		relayBody := map[string]any{
			"body": map[string]any{
				"account_id":    req.AccountId,
				"action_name":   actionName,
				"action_params": actionParams,
				"origin":        "workspace-relay",
			},
			"request_id": reqID,
			"no_sinks":   true,
		}
		payload, err := json.Marshal(relayBody)
		if err != nil {
			logger.Error("workspace: failed to marshal relay request", "err", err)
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "internal error"))
			return
		}

		// 6. RPC call via RabbitMQ to the k8s agent
		routingKey := mq.RelayQueueName(req.AccountId, "k8s")
		ctx, cancel := context.WithTimeout(c.Request.Context(), cfg.HTTP.WriteTimeout)
		defer cancel()

		logger.Info("workspace: publishing RPC", "tool", toolName, "corr_id", reqID, "action", actionName)
		respBytes, err := rpcClient.Call(ctx, cfg.RabbitMQ.ExchangeName, routingKey, payload, reqID)
		if err != nil {
			logger.Error("workspace: RPC call failed", "corr_id", reqID, "err", err)
			c.JSON(http.StatusGatewayTimeout, utils.BuildError(504, "timeout waiting for agent"))
			return
		}

		// 7. Parse the nested agent response and return clean stdout/stderr
		result, parseErr := extractWorkspaceResult(respBytes)
		if parseErr != nil {
			logger.Warn("workspace: response parse failed, returning raw", "corr_id", reqID, "err", parseErr)
			c.JSON(http.StatusOK, gin.H{"result": string(respBytes)})
			return
		}

		c.JSON(http.StatusOK, gin.H{"result": result})
	}
}

// parseWorkspaceToken validates the JWT and returns its claims.
func parseWorkspaceToken(tokenString, jwtSecret string) (*workspaceTokenClaims, error) {
	if jwtSecret == "" {
		return nil, fmt.Errorf("workspace JWT secret not configured")
	}
	token, err := jwt.ParseWithClaims(tokenString, &workspaceTokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(jwtSecret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	claims, ok := token.Claims.(*workspaceTokenClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	return claims, nil
}

// workspaceToolToIntegrationType maps the shim tool name to the integration type stored in the DB.
// Returns "" for tools that use the agent's own credentials (kubectl, helm).
func workspaceToolToIntegrationType(tool string) string {
	switch tool {
	case "psql", "postgres":
		return "postgresql"
	case "mysql":
		return "mysql"
	case "redis", "redis-cli":
		return "redis"
	case "argocd":
		return "argocd"
	case "clickhouse", "clickhouse-client":
		return "clickhouse"
	case "rabbitmq", "rabbitmqadmin":
		return "rabbitmq"
	case "ssh":
		return "ssh"
	case "mssql", "sqlcmd":
		return "mssql"
	case "oracle", "sqlplus":
		return "oracle"
	default:
		// kubectl, helm: use k8s agent's own RBAC — no integration needed
		return ""
	}
}

func configVal(values []db.WorkspaceConfigValue, name string) string {
	for _, v := range values {
		if v.Name == name {
			return v.Value
		}
	}
	return ""
}

func configExists(values []db.WorkspaceConfigValue, name string) bool {
	for _, v := range values {
		if v.Name == name {
			return true
		}
	}
	return false
}

// buildWorkspaceAction constructs the relay action name and params from a tool + command.
// This is a port of llm-server's tools.ExecuteContainerJob (raw=true path).
func buildWorkspaceAction(tool, command string, configValues []db.WorkspaceConfigValue, arguments map[string]any, shellImage string) (string, map[string]any, error) {
	podName := "nb-llm-" + uuid.NewString()

	// database argument (for postgres / mysql / clickhouse)
	dbName := ""
	if arguments != nil {
		if v, ok := arguments["database"].(string); ok {
			dbName = v
		}
	}

	switch tool {
	// ── kubectl: dedicated action, no image or secret needed ──────────────────
	case "kubectl":
		return "kubectl_command_executor", map[string]any{
			"command":  command,
			"pod_name": podName,
		}, nil

	// ── postgres ──────────────────────────────────────────────────────────────
	case "psql", "postgres":
		pgFlags := ""
		if dbName != "" {
			pgFlags = " --dbname " + dbName
		}
		if strings.HasPrefix(command, "psql") {
			command = strings.Replace(command, "psql", "psql"+pgFlags, 1)
		} else {
			command = fmt.Sprintf(`psql%s -c "%s"`, pgFlags, command)
		}
		params := map[string]any{
			"image":    shellImage,
			"command":  command,
			"pod_name": podName,
		}
		injectK8sSecret(params, configValues)
		return "pod_script_run_enricher", params, nil

	// ── mysql ─────────────────────────────────────────────────────────────────
	case "mysql":
		mysqlFlags := `--user=$MYSQL_USER --ssl=0 --password=$MYSQL_PASSWD --host=$MYSQL_HOST --port=$MYSQL_PORT`
		if dbName != "" {
			mysqlFlags += " --database=" + dbName
		}
		if strings.Contains(command, "mysql") {
			command = strings.Replace(command, "mysql", "mysql "+mysqlFlags, 1)
		} else if strings.Contains(command, "mariadb") {
			command = strings.Replace(command, "mariadb", "mariadb "+mysqlFlags, 1)
		}
		params := map[string]any{
			"image":    shellImage,
			"command":  command,
			"pod_name": podName,
		}
		injectK8sSecret(params, configValues)
		return "pod_script_run_enricher", params, nil

	// ── redis ─────────────────────────────────────────────────────────────────
	case "redis", "redis-cli":
		if strings.Contains(command, "redis-cli") {
			command = strings.Replace(command, "redis-cli", "redis-cli -h $REDIS_HOST --user $REDIS_USER --pass $REDIS_PASSWORD --no-auth-warning", 1)
		} else {
			command = "redis-cli -h $REDIS_HOST --user $REDIS_USER --pass $REDIS_PASSWORD --no-auth-warning " + command
		}
		params := map[string]any{
			"image":    shellImage,
			"command":  command,
			"pod_name": podName,
		}
		injectK8sSecret(params, configValues)
		return "pod_script_run_enricher", params, nil

	// ── clickhouse ────────────────────────────────────────────────────────────
	case "clickhouse", "clickhouse-client":
		chHost := configVal(configValues, "host")
		chPort := "9000"
		if v := configVal(configValues, "port"); v != "" {
			chPort = v
		}
		chDatabase := "default"
		if v := configVal(configValues, "database"); v != "" {
			chDatabase = v
		}
		if dbName != "" {
			chDatabase = dbName
		}
		// chUserKey/chPassKey: env var names referenced in the CLI command (--user $X)
		chUserKey := "CLICKHOUSE_USER"
		if v := configVal(configValues, "secret_user_key"); v != "" {
			chUserKey = v
		}
		chPassKey := "CLICKHOUSE_PASSWORD"
		if v := configVal(configValues, "secret_password_key"); v != "" {
			chPassKey = v
		}
		// secretUserKey/secretPassKey: k8s secret key names used for env injection
		secretUserKey := chUserKey
		if v := configVal(configValues, "user_key_in_secret"); v != "" {
			secretUserKey = v
		}
		secretPassKey := chPassKey
		if v := configVal(configValues, "password_key_in_secret"); v != "" {
			secretPassKey = v
		}
		chSecure := strings.ToLower(configVal(configValues, "secure_connection")) == "true"
		secureFlag := ""
		if chSecure {
			secureFlag = "--secure"
			if chPort == "9000" && !configExists(configValues, "port") {
				chPort = "9440"
			}
		}
		chFlags := fmt.Sprintf(`--host %s --port %s --user $%s --password $%s --database %s %s`,
			chHost, chPort, chUserKey, chPassKey, chDatabase, secureFlag)
		if strings.Contains(command, "clickhouse client") {
			command = strings.Replace(command, "clickhouse client", "clickhouse client "+chFlags, 1)
		} else if strings.Contains(command, "clickhouse-client") {
			// The shell image ships only the `clickhouse` multi-call binary
			// (no `clickhouse-client` symlink), so rewrite the hyphenated form
			// to the `clickhouse client` subcommand to invoke client mode.
			command = strings.Replace(command, "clickhouse-client", "clickhouse client "+chFlags, 1)
		}

		envFromSecret := map[string]string{secretUserKey: secretUserKey, secretPassKey: secretPassKey}
		params := map[string]any{
			"image":                shellImage,
			"command":              command,
			"pod_name":             podName,
			"env_from_secret_keys": envFromSecret,
		}
		injectK8sSecret(params, configValues)
		return "pod_script_run_enricher", params, nil

	// ── rabbitmq ──────────────────────────────────────────────────────────────
	case "rabbitmq", "rabbitmqadmin":
		if strings.Contains(command, "rabbitmqadmin") {
			command = strings.Replace(command, "rabbitmqadmin",
				"rabbitmqadmin --host $RABBITMQ_HOST --port $RABBITMQ_PORT --username $RABBITMQ_USER --password $RABBITMQ_PASSWORD", 1)
		} else if strings.Contains(command, "curl") && strings.Contains(command, "/api/") {
			command = strings.Replace(command, "curl ", "curl -s -u $RABBITMQ_USER:$RABBITMQ_PASSWORD ", 1)
			command = strings.ReplaceAll(command, "$RABBITMQ_PORT", "${RABBITMQ_MGMT_PORT:-15672}")
		}
		params := map[string]any{
			"image":    shellImage,
			"command":  command,
			"pod_name": podName,
		}
		injectK8sSecret(params, configValues)
		return "pod_script_run_enricher", params, nil

	// ── argocd ────────────────────────────────────────────────────────────────
	case "argocd":
		if !strings.HasPrefix(command, "argocd") {
			command = "argocd " + command
		}
		argoCDFlags := buildArgoCDFlagsFromConfig(configValues)
		if strings.Contains(command, "argocd app") || strings.Contains(command, "argocd proj") ||
			strings.Contains(command, "argocd cluster") || strings.Contains(command, "argocd repo") {
			command = strings.Replace(command, "argocd ", "argocd "+argoCDFlags+" ", 1)
		}
		authMethod := configVal(configValues, "auth_method")
		serverKey := "ARGOCD_SERVER"
		if v := configVal(configValues, "server_key_in_secret"); v != "" {
			serverKey = v
		}
		envFromSecret := map[string]string{serverKey: serverKey}
		if authMethod == "password" {
			usernameKey := "ARGOCD_USERNAME"
			if v := configVal(configValues, "username_key_in_secret"); v != "" {
				usernameKey = v
			}
			passwordKey := "ARGOCD_PASSWORD"
			if v := configVal(configValues, "password_key_in_secret"); v != "" {
				passwordKey = v
			}
			envFromSecret[usernameKey] = usernameKey
			envFromSecret[passwordKey] = passwordKey
		} else {
			authTokenKey := "ARGOCD_AUTH_TOKEN"
			if v := configVal(configValues, "auth_token_key_in_secret"); v != "" {
				authTokenKey = v
			}
			envFromSecret[authTokenKey] = authTokenKey
		}
		params := map[string]any{
			"image":                shellImage,
			"command":              command,
			"pod_name":             podName,
			"env_from_secret_keys": envFromSecret,
		}
		injectK8sSecret(params, configValues)
		return "pod_script_run_enricher", params, nil

	// ── ssh ───────────────────────────────────────────────────────────────────
	case "ssh":
		command = fmt.Sprintf(`mkdir -p ~/.ssh && echo "$SSH_KEY" > ~/.ssh/id_rsa && chmod 600 ~/.ssh/id_rsa && ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR $SSH_USER@$SSH_HOST "%s"`, command)
		params := map[string]any{
			"image":    shellImage,
			"command":  command,
			"pod_name": podName,
		}
		injectK8sSecret(params, configValues)
		return "pod_script_run_enricher", params, nil

	// ── oracle ───────────────────────────────────────────────────────────────
	case "oracle", "sqlplus":
		oracleService := "$ORACLE_SERVICE"
		if dbName != "" {
			oracleService = sanitizeNameRe.ReplaceAllString(dbName, "")
		}
		// The workspace shim sends: sqlplus [-d "db"] -Q "SQL"
		// Extract the SQL from -Q flag and database from -d flag.
		if oracleService == "$ORACLE_SERVICE" {
			if d := extractOracleDatabase(command); d != "" {
				oracleService = sanitizeNameRe.ReplaceAllString(d, "")
			}
		}
		sql := extractOracleSQL(command)
		sql = strings.TrimSpace(sql)
		sql = strings.TrimSuffix(sql, ";")
		// Prevent heredoc breakout
		lines := strings.Split(sql, "\n")
		var safeLines []string
		for _, line := range lines {
			if strings.TrimSpace(line) != "EOF" {
				safeLines = append(safeLines, line)
			}
		}
		sql = strings.Join(safeLines, "\n")
		command = fmt.Sprintf(
			`sqlplus -S $ORACLE_USER/$ORACLE_PASSWORD@//$ORACLE_HOST:${ORACLE_PORT:-1521}/%s <<'EOF'`+"\n"+
				`SET MARKUP CSV ON`+"\n"+
				`SET FEEDBACK OFF`+"\n"+
				`SET HEADING ON`+"\n"+
				`%s;`+"\n"+
				`EXIT;`+"\n"+
				`EOF`,
			oracleService, sql,
		)
		params := map[string]any{
			"image":    shellImage,
			"command":  command,
			"pod_name": podName,
		}
		injectK8sSecret(params, configValues)
		return "pod_script_run_enricher", params, nil

	// ── mssql ────────────────────────────────────────────────────────────────
	case "mssql", "sqlcmd":
		mssqlFlags := `-S "$MSSQL_HOST,$MSSQL_PORT" -U "$MSSQL_USER" -P "$MSSQL_PASSWORD"`
		if dbName != "" {
			safeDB := sanitizeNameRe.ReplaceAllString(dbName, "")
			mssqlFlags += ` -d "` + safeDB + `"`
		}
		if strings.Contains(command, "sqlcmd") {
			command = strings.Replace(command, "sqlcmd", "sqlcmd "+mssqlFlags, 1)
		} else {
			command = fmt.Sprintf(`sqlcmd %s -Q "%s" -s "	" -W`, mssqlFlags, command)
		}
		params := map[string]any{
			"image":    shellImage,
			"command":  command,
			"pod_name": podName,
		}
		injectK8sSecret(params, configValues)
		return "pod_script_run_enricher", params, nil

	// ── helm and other passthrough tools ──────────────────────────────────────
	case "helm":
		params := map[string]any{
			"image":    shellImage,
			"command":  command,
			"pod_name": podName,
		}
		injectK8sSecret(params, configValues)
		return "pod_script_run_enricher", params, nil

	default:
		return "", nil, fmt.Errorf("unsupported tool: %s", tool)
	}
}

// injectK8sSecret adds namespace and secret to params from the k8s_secret config value.
func injectK8sSecret(params map[string]any, configValues []db.WorkspaceConfigValue) {
	k8sSecret := configVal(configValues, "k8s_secret")
	if k8sSecret == "" {
		return
	}
	if strings.Contains(k8sSecret, "/") {
		parts := strings.SplitN(k8sSecret, "/", 2)
		params["namespace"] = parts[0]
		params["secret"] = parts[1]
	} else {
		params["secret"] = k8sSecret
	}
}

// buildArgoCDFlagsFromConfig builds the ArgoCD CLI flags from integration config values.
func buildArgoCDFlagsFromConfig(configValues []db.WorkspaceConfigValue) string {
	var flags []string
	flags = append(flags, "--server $ARGOCD_SERVER", "--auth-token $ARGOCD_AUTH_TOKEN")

	insecure := true
	if v := configVal(configValues, "insecure"); v != "" {
		insecure = strings.ToLower(v) == "true"
	}
	if insecure {
		flags = append(flags, "--insecure")
	}
	if strings.ToLower(configVal(configValues, "grpc_web")) == "true" {
		flags = append(flags, "--grpc-web")
	}
	if timeout := configVal(configValues, "timeout"); timeout != "" && timeout != "30" {
		flags = append(flags, "--request-timeout", timeout+"s")
	}
	if cfgPath := configVal(configValues, "config_file_path"); cfgPath != "" {
		flags = append(flags, "--config", cfgPath)
	}
	return strings.Join(flags, " ")
}

// extractOracleSQL parses a workspace sqlplus command and returns the SQL.
// Expected format: sqlplus [-d "database"] -Q "SQL"
func extractOracleSQL(command string) string {
	lower := strings.ToLower(command)
	qIdx := strings.Index(lower, " -q ")
	if qIdx < 0 {
		return command
	}
	return extractQuotedArg(command[qIdx+4:])
}

// extractOracleDatabase parses a workspace sqlplus command and returns the database name.
func extractOracleDatabase(command string) string {
	lower := strings.ToLower(command)
	dIdx := strings.Index(lower, " -d ")
	if dIdx < 0 {
		return ""
	}
	return extractQuotedArg(command[dIdx+4:])
}

// extractQuotedArg extracts a possibly-quoted argument, handling \" escapes.
func extractQuotedArg(s string) string {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return ""
	}
	if s[0] != '"' {
		if idx := strings.IndexByte(s, ' '); idx >= 0 {
			return s[:idx]
		}
		return s
	}
	var result strings.Builder
	i := 1 // skip opening quote
	for i < len(s) {
		if s[i] == '\\' && i+1 < len(s) && s[i+1] == '"' {
			result.WriteByte('"')
			i += 2
			continue
		}
		if s[i] == '"' {
			break
		}
		result.WriteByte(s[i])
		i++
	}
	return result.String()
}

// extractWorkspaceResult navigates the deeply nested agent response envelope and
// returns the plain stdout+stderr string. This is a port of llm-server's
// getRelayCommandResponseData (raw=true path).
func extractWorkspaceResult(respBytes []byte) (string, error) {
	var relayResponse map[string]any
	if err := json.Unmarshal(respBytes, &relayResponse); err != nil {
		return "", fmt.Errorf("parse agent response: %w", err)
	}

	data1, ok := relayResponse["data"].(map[string]any)
	if !ok || data1 == nil {
		return "", fmt.Errorf("missing data field in agent response")
	}

	findings, ok := data1["findings"].([]any)
	if !ok || len(findings) == 0 {
		return "", fmt.Errorf("missing findings in agent response")
	}

	firstFinding, ok := findings[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid findings format")
	}

	evidenceRaw, ok := firstFinding["evidence"].([]any)
	if !ok || len(evidenceRaw) == 0 {
		return "", fmt.Errorf("missing evidence in agent response")
	}

	firstEvidence, ok := evidenceRaw[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid evidence format")
	}

	evidenceData, ok := firstEvidence["data"].(string)
	if !ok {
		return "", fmt.Errorf("missing evidence data string")
	}

	var commandResponseArray []any
	if err := json.Unmarshal([]byte(evidenceData), &commandResponseArray); err != nil {
		return "", fmt.Errorf("parse command response array: %w", err)
	}
	if len(commandResponseArray) == 0 {
		return "", nil
	}

	firstResponse, ok := commandResponseArray[0].(map[string]any)
	if !ok {
		return "", fmt.Errorf("invalid command response format")
	}

	firstResponseData, ok := firstResponse["data"].(string)
	if !ok {
		return "", fmt.Errorf("missing response data string")
	}

	var commandResponse map[string]any
	if err := json.Unmarshal([]byte(firstResponseData), &commandResponse); err != nil {
		return "", fmt.Errorf("parse command response: %w", err)
	}

	responseAny, ok := commandResponse["response"]
	if !ok {
		return "", nil
	}

	responseStr, ok := responseAny.(string)
	if !ok {
		b, _ := json.Marshal(responseAny)
		responseStr = string(b)
	}

	// If the response is JSON with stdout/stderr fields, extract and combine them.
	trimmed := strings.TrimSpace(responseStr)
	if strings.HasPrefix(trimmed, "{") {
		var execResult map[string]any
		if err := json.Unmarshal([]byte(trimmed), &execResult); err == nil {
			stdout, _ := execResult["stdout"].(string)
			stderr, _ := execResult["stderr"].(string)
			if stdout != "" || stderr != "" {
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
