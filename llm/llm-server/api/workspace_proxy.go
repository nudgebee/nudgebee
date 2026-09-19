package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/shlex"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// newWorkspaceManager is the constructor used by workspace file handlers.
// It's a package-level var so tests can swap in a fake WorkspaceManager.
var newWorkspaceManager = workspace.NewWorkspaceManager

type WorkspaceExecuteRequest struct {
	AccountId  string         `json:"account_id" binding:"required"`
	Tool       string         `json:"tool" binding:"required"`
	Command    string         `json:"command"`
	Arguments  map[string]any `json:"arguments"`
	ConfigName string         `json:"config_name"`
	TargetName string         `json:"target_name"`
}

type WorkspaceListFilesRequest struct {
	AccountId      string `json:"account_id" binding:"required"`
	Path           string `json:"path"`
	ConversationId string `json:"conversation_id"`
}

type WorkspaceGetFileRequest struct {
	AccountId      string `json:"account_id" binding:"required"`
	Path           string `json:"path" binding:"required"`
	ConversationId string `json:"conversation_id"`
}

type WorkspaceDeleteFileRequest struct {
	AccountId      string `json:"account_id" binding:"required"`
	Path           string `json:"path" binding:"required"`
	ConversationId string `json:"conversation_id"`
}

type WorkspaceBatchReadFileRequest struct {
	AccountId      string   `json:"account_id" binding:"required"`
	Paths          []string `json:"paths" binding:"required"`
	ConversationId string   `json:"conversation_id"`
}

type WorkspaceSaveFileRequest struct {
	AccountId      string `json:"account_id" binding:"required"`
	Path           string `json:"path" binding:"required"`
	Content        string `json:"content"`
	ConversationId string `json:"conversation_id"`
}

func handleWorkspaceApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	group := r.Group("/api/v1/workspace")

	// Unified Files API (Handles all RPC Actions: list, get, save, delete, batch-read)
	group.POST("/files", func(c *gin.Context) { handleWorkspaceFiles(c, tracer, meter) })

	// Tool Execution
	group.POST("/execute", func(c *gin.Context) { handleWorkspaceExecute(c, tracer, meter) })
}

func handleWorkspaceFiles(c *gin.Context, tracer trace.Tracer, meter metric.Meter) {
	payload, ctx, actionName, err := extractWorkspacePayloadAndContext(c, tracer, meter)
	if err != nil {
		c.JSON(http.StatusUnauthorized, common.ErrorActionBadRequest(err.Error()))
		return
	}

	switch actionName {
	case "ai_list_workspace_files":
		handleWorkspaceListFilesWithPayload(c, payload, ctx)
	case "ai_get_workspace_file":
		handleWorkspaceGetFileWithPayload(c, payload, ctx)
	case "ai_save_workspace_file":
		handleWorkspaceSaveFileWithPayload(c, payload, ctx)
	case "ai_delete_workspace_file":
		handleWorkspaceDeleteFileWithPayload(c, payload, ctx)
	case "ai_batch_read_workspace_files":
		handleWorkspaceBatchReadFileWithPayload(c, payload, ctx)
	default:
		c.JSON(http.StatusBadRequest, common.ErrorActionBadRequest(fmt.Sprintf("unsupported action: %s", actionName)))
	}
}

func extractWorkspacePayloadAndContext(c *gin.Context, tracer trace.Tracer, meter metric.Meter) (map[string]any, *security.RequestContext, string, error) {
	// 1. Try Global Token Auth (X-ACTION-TOKEN)
	authHeader := c.Request.Header.Get(config.Config.LlmServerTokenHeader)
	if authHeader != "" && authHeader == config.Config.LlmServerToken {
		requestMap := make(map[string]any)
		if err := c.ShouldBindJSON(&requestMap); err != nil {
			return nil, nil, "", err
		}

		var actionRequest ActionRequest
		_ = common.DecodeMapToStruct(requestMap, &actionRequest)

		payload := requestMap
		actionName := actionRequest.Action.Name
		if actionName != "" {
			payload = actionRequest.Input
			if rawRequest, ok := payload["request"]; ok {
				if castedRequest, castOk := rawRequest.(map[string]any); castOk {
					payload = castedRequest
				}
			}
		}

		ctx := security.NewRequestContext(c.Request.Context(), security.NewSecurityContextForSuperAdmin(), slog.Default(), tracer, meter)
		return payload, ctx, actionName, nil
	}

	// 2. Standard Auth Flow
	requestMap := make(map[string]any)
	if err := c.ShouldBindJSON(&requestMap); err != nil {
		return nil, nil, "", err
	}

	var actionRequest ActionRequest
	_ = common.DecodeMapToStruct(requestMap, &actionRequest)

	var ctx *security.RequestContext
	var err error
	payload := requestMap
	actionName := actionRequest.Action.Name

	if actionName != "" {
		ctx, err = buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.Default())
		payload = actionRequest.Input
		if rawRequest, ok := payload["request"]; ok {
			if castedRequest, castOk := rawRequest.(map[string]any); castOk {
				payload = castedRequest
			}
		}
	} else {
		accountId, _ := requestMap["account_id"].(string)
		ctx, err = authorizeWorkspaceRequest(c, accountId, tracer, meter)
	}

	if err != nil {
		return nil, nil, "", err
	}

	accountId, _ := payload["account_id"].(string)
	if accountId != "" && !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) &&
		!granted(ctx.GetSecurityContext(), accountId, moduleAiMisc, "Read", "Write") {
		return nil, nil, "", errors.New(errorUserAccessMessage)
	}

	return payload, ctx, actionName, nil
}

func authorizeWorkspaceRequest(c *gin.Context, requestedAccountId string, tracer trace.Tracer, meter metric.Meter) (*security.RequestContext, error) {
	tokenString := c.GetHeader("X-Workspace-Token")
	if tokenString == "" {
		return nil, fmt.Errorf("missing authentication token")
	}

	token, err := jwt.ParseWithClaims(tokenString, &workspace.WorkspaceTokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(config.Config.LlmServerJwtSecret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("invalid or expired token: %w", err)
	}

	tenantId, err := security.GetTenantIdFromAccountId(requestedAccountId)
	if err != nil {
		return nil, fmt.Errorf("invalid account id")
	}

	claims, ok := token.Claims.(*workspace.WorkspaceTokenClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}
	if _, found := common.CacheGet(workspace.CacheNamespaceWorkspaceTokens, tokenString); found {
		return nil, fmt.Errorf("token has been revoked")
	}
	if claims.AccountId != requestedAccountId {
		return nil, fmt.Errorf("token does not match account")
	}
	if claims.TenantId != "" && claims.TenantId != tenantId {
		return nil, fmt.Errorf("token does not match tenant")
	}

	isExecutionToken := slices.Contains(claims.Audience, workspace.AudienceWorkspaceExecute)
	if isExecutionToken != (claims.Grants != nil) {
		return nil, fmt.Errorf("invalid token claims")
	}
	if !isExecutionToken {
		// The pod's own token (or one issued before audiences existed).
		c.Set("workspace_allowed_target", claims.AccountId)
		c.Set(workspaceTokenAudienceKey, workspace.AudienceWorkspacePod)
		return security.NewRequestContextForTenantAdmin(tenantId), nil
	}
	c.Set(workspaceTokenAudienceKey, workspace.AudienceWorkspaceExecute)
	if claims.TenantId == "" {
		return nil, fmt.Errorf("token does not match tenant")
	}
	sc, err := security.NewSecurityContextFromWorkspaceGrants(tenantId, *claims.Grants)
	if err != nil {
		return nil, fmt.Errorf("invalid token grants: %w", err)
	}
	if claims.TargetAccountId != "" && !sc.HasAccountAccess(claims.TargetAccountId, security.SecurityAccessTypeRead) {
		return nil, fmt.Errorf("token does not permit its target")
	}
	if claims.TargetAccountId != "" {
		c.Set("workspace_allowed_target", claims.TargetAccountId)
	}
	return security.NewRequestContext(c.Request.Context(), sc, slog.Default(), tracer, meter), nil
}

// workspaceTokenAudienceKey records which kind of workspace token
// authenticated the request (set only on the workspace-token path).
const workspaceTokenAudienceKey = "workspace_token_audience"

// requireExecutionToken enforces, and counts, that tool execution through the
// workspace token path uses a per-command execution token rather than the
// pod's own token. Requests authenticated another way (action gateway, global
// token) carry no workspace token and are not affected. It reports whether the
// request may continue.
func requireExecutionToken(c *gin.Context, ctx *security.RequestContext, accountId string) bool {
	value, viaWorkspaceToken := c.Get(workspaceTokenAudienceKey)
	if !viaWorkspaceToken {
		return true
	}
	audience, _ := value.(string)
	enforced := config.Config.LlmServerWorkspaceExecuteRequireScopedToken
	allowed := audience == workspace.AudienceWorkspaceExecute || !enforced
	recordWorkspaceExecuteAuth(c.Request.Context(), audience, enforced, allowed)
	if audience == workspace.AudienceWorkspaceExecute {
		return true
	}
	ctx.GetLogger().Warn("workspace: execute called with the pod token", "account_id", accountId, "enforced", enforced)
	if !allowed {
		c.JSON(http.StatusUnauthorized, common.ErrorActionBadRequest("workspace execution requires a per-command execution token; run the command through a Nudgebee tool"))
	}
	return allowed
}

func handleWorkspaceListFilesWithPayload(c *gin.Context, payload map[string]any, ctx *security.RequestContext) {
	var req WorkspaceListFilesRequest
	_ = common.DecodeMapToStruct(payload, &req)

	files, err := newWorkspaceManager().ListFiles(ctx, req.AccountId, req.ConversationId, req.Path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.ErrorActionInternal(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": files})
}

func handleWorkspaceGetFileWithPayload(c *gin.Context, payload map[string]any, ctx *security.RequestContext) {
	var req WorkspaceGetFileRequest
	_ = common.DecodeMapToStruct(payload, &req)

	reader, err := newWorkspaceManager().ReadFileStream(ctx, req.AccountId, req.ConversationId, req.Path)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(err.Error(), "404") {
			c.JSON(http.StatusNotFound, common.ErrorActionBadRequest("File not found"))
		} else {
			c.JSON(http.StatusInternalServerError, common.ErrorActionInternal(err.Error()))
		}
		return
	}
	defer func() { _ = reader.Close() }()

	content, status, errMsg := readWorkspaceFileForDownload(reader, workspaceFileMaxDownloadBytes())
	if status != http.StatusOK {
		ctx.GetLogger().Warn("workspace: get file rejected",
			"status", status, "path", req.Path, "error", errMsg)
		if status >= http.StatusInternalServerError {
			c.JSON(status, common.ErrorActionInternal("failed to read workspace file"))
		} else {
			c.JSON(status, common.ErrorActionBadRequest(errMsg))
		}
		return
	}

	// The RPC action `ai_get_workspace_file` returns `jsonb`, so the webhook
	// response MUST be a valid JSON value. Return the file content as a JSON
	// string — the frontend handles the string case and builds the download
	// blob itself (see ReferencesModal.handleDownloadFile).
	//
	// NOTE: this endpoint is text-only. Binary content is rejected upstream by
	// readWorkspaceFileForDownload because json.Marshal coerces strings to
	// valid UTF-8 and would silently corrupt non-UTF-8 bytes.
	c.JSON(http.StatusOK, content)
}

// workspaceFileMaxDownloadBytes returns the configured cap for the
// ai_get_workspace_file handler, falling back to 5 MiB if the config value is
// missing or non-positive. Isolated so tests can reason about it.
func workspaceFileMaxDownloadBytes() int64 {
	const fallback int64 = 5 * 1024 * 1024
	if v := config.Config.LlmServerWorkspaceFileMaxDownloadBytes; v > 0 {
		return int64(v)
	}
	return fallback
}

// readWorkspaceFileForDownload drains a workspace file reader into a string
// suitable for returning from the ai_get_workspace_file RPC action. It
// enforces two invariants that the RPC path requires:
//
//  1. Peak memory is bounded: the reader is wrapped in io.LimitReader with
//     maxBytes+1 so an overrun is detectable instead of silently truncating.
//     Files larger than maxBytes are rejected with 413.
//  2. The content is valid UTF-8: json.Marshal would otherwise replace
//     invalid bytes with U+FFFD and silently corrupt the file. Binary
//     content is rejected with 415.
//
// On success the returned status is http.StatusOK, content holds the file
// bytes as a string, and errMsg is empty. On failure content is empty and
// errMsg is a human-readable reason safe to surface to the caller.
func readWorkspaceFileForDownload(reader io.Reader, maxBytes int64) (content string, status int, errMsg string) {
	limited := io.LimitReader(reader, maxBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return "", http.StatusInternalServerError, fmt.Sprintf("failed to read file: %v", err)
	}
	if int64(len(raw)) > maxBytes {
		return "", http.StatusRequestEntityTooLarge, fmt.Sprintf("file exceeds %d-byte download limit", maxBytes)
	}
	if !utf8.Valid(raw) {
		return "", http.StatusUnsupportedMediaType, "file is not valid UTF-8; this endpoint only supports text files"
	}
	return string(raw), http.StatusOK, ""
}

func handleWorkspaceBatchReadFileWithPayload(c *gin.Context, payload map[string]any, ctx *security.RequestContext) {
	var req WorkspaceBatchReadFileRequest
	_ = common.DecodeMapToStruct(payload, &req)

	files, err := newWorkspaceManager().BatchReadFile(ctx, req.AccountId, req.ConversationId, req.Paths)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.ErrorActionInternal(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": files})
}

func handleWorkspaceSaveFileWithPayload(c *gin.Context, payload map[string]any, ctx *security.RequestContext) {
	var req WorkspaceSaveFileRequest
	_ = common.DecodeMapToStruct(payload, &req)

	err := newWorkspaceManager().SaveFile(ctx, req.AccountId, req.ConversationId, req.Path, req.Content)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.ErrorActionInternal(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "path": req.Path})
}

func handleWorkspaceDeleteFileWithPayload(c *gin.Context, payload map[string]any, ctx *security.RequestContext) {
	var req WorkspaceDeleteFileRequest
	_ = common.DecodeMapToStruct(payload, &req)

	err := newWorkspaceManager().DeleteFile(ctx, req.AccountId, req.ConversationId, req.Path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, common.ErrorActionInternal(err.Error()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "success", "path": req.Path})
}

func handleWorkspaceExecute(c *gin.Context, tracer trace.Tracer, meter metric.Meter) {
	payload, ctx, _, err := extractWorkspacePayloadAndContext(c, tracer, meter)
	if err != nil {
		c.JSON(http.StatusUnauthorized, common.ErrorActionBadRequest(err.Error()))
		return
	}

	var req WorkspaceExecuteRequest
	_ = common.DecodeMapToStruct(payload, &req)
	if !requireExecutionToken(c, ctx, req.AccountId) {
		return
	}

	var relayJob tools.RelayJob
	var registeredToolName string
	toolName := strings.ToLower(req.Tool)
	if toolName == "kubectl" {
		if normalizeErr := normalizeKubectlProxyRequest(&req); normalizeErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": normalizeErr.Error()})
			return
		}
	}

	switch toolName {
	case "kubectl":
		relayJob = tools.RelayJobKubectl
		registeredToolName = tools.ToolExecuteKubectlCommand
	case "helm":
		relayJob = tools.RelayJobHelm
		registeredToolName = tools.ToolExecuteHelmCommand
	case "psql", "postgres":
		relayJob = tools.RelayJobPostgres
		registeredToolName = tools.ToolExecutePostgresQuery
	case "mysql":
		relayJob = tools.RelayJobMysql
		registeredToolName = tools.ToolExecuteMySQLQuery
	case "redis", "redis-cli":
		relayJob = tools.RelayJobRedis
		registeredToolName = tools.ToolExecuteRedisCommand
	case "argocd":
		relayJob = tools.RelayJobArgoCD
		registeredToolName = tools.ToolExecuteArgoCDCommand
	case "clickhouse", "clickhouse-client":
		relayJob = tools.RelayJobClickhouse
		registeredToolName = tools.ToolExecuteClickhouseQuery
	case "rabbitmq", "rabbitmqadmin", "rabbitmq-api":
		relayJob = tools.RelayJobRabbitmq
		registeredToolName = tools.ToolExecuteRabbitCommand
	case "mssql", "sqlcmd":
		relayJob = tools.RelayJobMssql
		registeredToolName = tools.ToolExecuteMSSQLQuery
	case "oracle", "sqlplus":
		relayJob = tools.RelayJobOracle
		registeredToolName = tools.ToolExecuteOracleQuery
	case "ssh":
		relayJob = tools.RelayJobSSH
		registeredToolName = tools.ToolExecuteServerCommand
	default:
		c.JSON(http.StatusBadRequest, common.ErrorActionBadRequest(fmt.Sprintf("unsupported tool: %s", req.Tool)))
		return
	}

	var nbTool core.NBTool
	if registeredToolName != "" {
		var found bool
		nbTool, found = core.GetNBTool(req.AccountId, registeredToolName)
		if !found {
			nbTool, found = core.GetNBTool(req.AccountId, toolName)
			if !found {
				c.JSON(http.StatusNotFound, common.ErrorActionBadRequest(fmt.Sprintf("tool not found: %s", req.Tool)))
				return
			}
		}
	} else {
		c.JSON(http.StatusBadRequest, common.ErrorActionBadRequest(fmt.Sprintf("tool %s is not fully supported via proxy yet", req.Tool)))
		return
	}

	var queryConfig core.NBQueryConfig
	if req.ConfigName != "" {
		queryConfig = core.NBQueryConfig{
			ToolConfigs: map[string]string{nbTool.Name(): req.ConfigName},
		}
	}

	toolCtx := core.NewNbToolContext(ctx, nbTool, req.AccountId, "system", "", "", "", req.Command, nil, "", queryConfig, "")
	if toolName == "kubectl" {
		if result, handled, configErr := virtualKubectlConfig(ctx, req, toolCtx.ToolConfig.Name); handled {
			if configErr != nil {
				writeWorkspaceExecuteAudit(ctx, req, toolName, req.AccountId, false)
				c.JSON(http.StatusBadRequest, gin.H{"error": configErr.Error()})
				return
			}
			writeWorkspaceExecuteAudit(ctx, req, toolName, req.AccountId, true)
			c.JSON(http.StatusOK, gin.H{"result": result})
			return
		}
	}

	// The pod/token remains bound to req.AccountId; Kubernetes execution uses
	// the selected cluster account, not the workspace's account.
	requestedTarget := req.TargetName
	if requestedTarget == "" {
		requestedTarget = req.ConfigName
	}
	targetAccountId, targetErr := workspaceRelayTarget(toolCtx, relayJob, requestedTarget, req.Command)
	if targetErr != nil {
		writeWorkspaceExecuteAudit(ctx, req, toolName, req.AccountId, false)
		c.JSON(http.StatusForbidden, gin.H{"error": targetErr.Error()})
		return
	}
	if allowed, bound := c.Get("workspace_allowed_target"); bound && targetAccountId != allowed {
		writeWorkspaceExecuteAudit(ctx, req, toolName, targetAccountId, false)
		c.JSON(http.StatusForbidden, gin.H{"error": "workspace token does not permit the selected target"})
		return
	}
	result, err := tools.ExecuteContainerJob(toolCtx, relayJob, req.Command, targetAccountId, req.Arguments, true)
	writeWorkspaceExecuteAudit(ctx, req, toolName, targetAccountId, err == nil)
	c.JSON(workspaceExecuteReply(result, err))
}

func writeWorkspaceExecuteAudit(ctx *security.RequestContext, req WorkspaceExecuteRequest, toolName, targetAccountId string, success bool) {
	auditReq := workspaceExecuteAudit(ctx, req, toolName, targetAccountId, success)
	go func() { _ = audit.CreateAudit(ctx, auditReq) }()
}

func workspaceExecuteAudit(ctx *security.RequestContext, req WorkspaceExecuteRequest, toolName, targetAccountId string, success bool) *audit.AuditRequest {
	var tenantId, userId string
	if sc := ctx.GetSecurityContext(); sc != nil {
		tenantId = sc.GetTenantId()
		userId = sc.GetUserId()
	}
	status := audit.EventStatusSuccess
	if !success {
		status = audit.EventStatusFailure
	}
	return &audit.AuditRequest{Audits: []audit.Audit{{
		AccountId:     targetAccountId,
		TenantId:      tenantId,
		UserId:        userId,
		EventTime:     time.Now(),
		EventCategory: audit.EventCategoryK8sRelay,
		EventType:     audit.EventTypeK8sRelayTask,
		EventActor:    audit.EventActorK8sAgent,
		EventAction:   audit.EventActionCreate,
		EventTarget:   toolName,
		EventStatus:   status,
		EventState:    map[string]any{"command": req.Command},
		EventAttr: map[string]any{
			"command":              req.Command,
			"tool":                 req.Tool,
			"arguments":            req.Arguments,
			"source":               "workspace-shim",
			"workspace_account_id": req.AccountId,
			"target_context":       req.TargetName,
		},
	}}}
}

func virtualKubectlConfig(ctx *security.RequestContext, req WorkspaceExecuteRequest, defaultContext string) (string, bool, error) {
	argv, err := kubectlCommandArgs(req.Command)
	if err != nil {
		return "", true, err
	}
	configIndex := kubectlConfigIndex(argv)
	if configIndex < 0 {
		return "", false, nil
	}
	if configIndex+1 >= len(argv) {
		return "", true, fmt.Errorf("kubectl config requires a subcommand")
	}
	subcommand := argv[configIndex+1]
	if subcommand != "current-context" && subcommand != "get-contexts" && subcommand != "view" {
		return "", true, fmt.Errorf("kubectl config %s is unavailable: workspace kubeconfig is virtual and read-only", subcommand)
	}
	contexts, err := tools.ListKubernetesTargets(ctx, req.AccountId)
	if err != nil {
		return "", true, fmt.Errorf("unable to list Kubernetes contexts: %w", err)
	}
	current := req.TargetName
	if current == "" {
		current = req.ConfigName
	}
	if current == "" {
		current = defaultContext
	}
	if current != "" {
		found := false
		for _, target := range contexts {
			if strings.EqualFold(target.Name, current) {
				current = target.Name
				found = true
				break
			}
		}
		if !found {
			return "", true, fmt.Errorf("kubernetes context %q is not connected or you do not have access", current)
		}
	}
	if subcommand == "current-context" {
		if current == "" {
			return "", true, fmt.Errorf("current-context is not set")
		}
		return current + "\n", true, nil
	}
	if subcommand == "get-contexts" {
		if outputFlag(argv) == "name" {
			var names []string
			for _, target := range contexts {
				names = append(names, target.Name)
			}
			return strings.Join(names, "\n") + trailingNewline(names), true, nil
		}
		var b strings.Builder
		b.WriteString("CURRENT   NAME   CLUSTER   AUTHINFO   NAMESPACE\n")
		for _, target := range contexts {
			marker := " "
			if strings.EqualFold(target.Name, current) {
				marker = "*"
			}
			fmt.Fprintf(&b, "%s         %s   %s   nubi-user   \n", marker, target.Name, target.Name)
		}
		return b.String(), true, nil
	}

	config := map[string]any{"apiVersion": "v1", "kind": "Config", "current-context": current}
	var entries []map[string]any
	var clusters []map[string]any
	for _, target := range contexts {
		entries = append(entries, map[string]any{"name": target.Name, "context": map[string]string{"cluster": target.Name, "user": "nubi-user"}})
		clusters = append(clusters, map[string]any{"name": target.Name, "cluster": map[string]string{"server": "nubi://" + target.Name}})
	}
	config["contexts"] = entries
	config["clusters"] = clusters
	config["users"] = []map[string]any{{"name": "nubi-user", "user": map[string]any{}}}
	if outputFlag(argv) == "json" {
		encoded, marshalErr := json.MarshalIndent(config, "", "  ")
		return string(encoded) + "\n", true, marshalErr
	}
	encoded, marshalErr := common.EncodeYaml(config)
	return string(encoded), true, marshalErr
}

// normalizeKubectlProxyRequest parses the concrete kubectl command forwarded
// by the shim, removes the context selector used for server-side target
// resolution, and safely reconstructs the single command sent to relay.
func normalizeKubectlProxyRequest(req *WorkspaceExecuteRequest) error {
	argv, err := kubectlCommandArgs(req.Command)
	if err != nil {
		return err
	}
	stripped, target, err := extractKubectlContextArgs(argv)
	if err != nil {
		return err
	}
	req.TargetName = target
	req.Command = buildProxyCommand("kubectl", stripped)
	return nil
}

func kubectlCommandArgs(command string) ([]string, error) {
	parts, err := shlex.Split(strings.TrimSpace(command))
	if err != nil {
		return nil, fmt.Errorf("unable to parse kubectl command: %w", err)
	}
	if len(parts) == 0 || filepath.Base(parts[0]) != "kubectl" {
		return nil, fmt.Errorf("kubectl command must start with kubectl")
	}
	return parts[1:], nil
}

func extractKubectlContextArgs(argv []string) ([]string, string, error) {
	stripped := make([]string, 0, len(argv))
	target := ""
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--context":
			if i+1 >= len(argv) || argv[i+1] == "" {
				return nil, "", fmt.Errorf("kubectl --context requires a value")
			}
			if target != "" && !strings.EqualFold(target, argv[i+1]) {
				return nil, "", fmt.Errorf("kubectl command names more than one context")
			}
			target = argv[i+1]
			i++
		case strings.HasPrefix(arg, "--context="):
			value := strings.TrimPrefix(arg, "--context=")
			if value == "" {
				return nil, "", fmt.Errorf("kubectl --context requires a value")
			}
			if target != "" && !strings.EqualFold(target, value) {
				return nil, "", fmt.Errorf("kubectl command names more than one context")
			}
			target = value
		default:
			stripped = append(stripped, arg)
		}
	}
	return stripped, target, nil
}

func buildProxyCommand(tool string, argv []string) string {
	parts := make([]string, 0, len(argv)+1)
	parts = append(parts, tool)
	for _, arg := range argv {
		parts = append(parts, common.ShellEscape(arg))
	}
	return strings.Join(parts, " ")
}

var kubectlConfigGlobalFlagsWithValue = map[string]bool{
	"--as": true, "--as-group": true, "--as-uid": true, "--cache-dir": true,
	"--certificate-authority": true, "--client-certificate": true, "--client-key": true,
	"--cluster": true, "--context": true, "--kubeconfig": true, "--namespace": true,
	"--password": true, "--profile": true, "--profile-output": true, "--request-timeout": true,
	"--server": true, "--tls-server-name": true, "--token": true, "--user": true,
	"-n": true, "-v": true, "--v": true, "--vmodule": true,
}

func kubectlConfigIndex(argv []string) int {
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		if !strings.HasPrefix(arg, "-") {
			if arg == "config" {
				return i
			}
			return -1
		}
		if strings.Contains(arg, "=") || arg == "--help" || arg == "-h" || arg == "--version" {
			continue
		}
		if kubectlConfigGlobalFlagsWithValue[arg] && i+1 < len(argv) {
			i++
		}
	}
	return -1
}

func outputFlag(argv []string) string {
	for i, arg := range argv {
		if strings.HasPrefix(arg, "--output=") {
			return strings.TrimPrefix(arg, "--output=")
		}
		if strings.HasPrefix(arg, "-o=") {
			return strings.TrimPrefix(arg, "-o=")
		}
		if (arg == "--output" || arg == "-o") && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func trailingNewline(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return "\n"
}

// workspaceExecuteReply maps a relay execution result to the shim's HTTP reply.
// The shim prints `result`, then exits 1 when `error` is set; a non-200 makes it
// print the raw body instead of the output.
//
// A command that ran and exited non-zero therefore gets 200 with both fields:
// the output still reaches the caller, and the workspace (and kubectl_execute's
// status) sees a failure instead of a success.
func workspaceExecuteReply(result any, err error) (int, gin.H) {
	var exitErr *tools.RelayCommandExitError
	if errors.As(err, &exitErr) {
		return http.StatusOK, gin.H{"result": result, "error": exitErr.Error()}
	}
	if err != nil {
		return http.StatusInternalServerError, gin.H{"error": err.Error(), "result": result}
	}
	return http.StatusOK, gin.H{"result": result}
}

func workspaceRelayTarget(ctx core.NbToolContext, job tools.RelayJob, requestedConfig string, command string) (string, error) {
	if job != tools.RelayJobKubectl {
		return ctx.AccountId, nil
	}
	selected := ctx.ToolConfig
	if requestedConfig == "" {
		// The workspace's own cluster: still checked, so a read-only user's
		// write is refused here and not only by the planner's approval gate.
		selected = core.ToolConfig{Values: []core.ToolConfigValue{{Name: "id", Value: ctx.AccountId}}}
	} else if selected.Name != requestedConfig {
		// The per-account config list is narrowed to the workspace's own
		// cluster, so a per-call target on another cluster is looked up by name
		// across the tenant. The caller still checks the result against the
		// token's bound target.
		if ctx.Ctx == nil || ctx.Ctx.GetSecurityContext() == nil {
			return "", fmt.Errorf("target cluster access denied")
		}
		found, err := tools.KubernetesTargetByName(ctx.Ctx, ctx.AccountId, requestedConfig)
		if err != nil {
			return "", fmt.Errorf("selected cluster configuration is unavailable")
		}
		selected = found
	}
	requiredAccess := security.SecurityAccessTypeRead
	if command != "" && tools.InferKubectlVerbType(command) != core.ToolRequestTypeRead {
		requiredAccess = security.SecurityAccessTypeCreate
	}
	for _, value := range selected.Values {
		if value.Name == "id" && value.Value != "" {
			if ctx.Ctx == nil || ctx.Ctx.GetSecurityContext() == nil {
				return "", fmt.Errorf("target cluster access denied")
			}
			// Same union that admits the user to run Nubi (and remediation) on
			// the account: a built-in role, or a custom-role grant to run AI.
			sc := ctx.Ctx.GetSecurityContext()
			if !sc.HasAccountAccess(value.Value, requiredAccess) && !grantedRun(sc, value.Value, moduleAiMisc) {
				return "", fmt.Errorf("target cluster access denied")
			}
			return value.Value, nil
		}
	}
	return "", fmt.Errorf("selected cluster configuration has no account id")
}
