package api

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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
	if accountId != "" && !ctx.GetSecurityContext().HasAccountAccess(accountId, security.SecurityAccessTypeRead) {
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

	if claims, ok := token.Claims.(*workspace.WorkspaceTokenClaims); ok && token.Valid {
		if _, found := common.CacheGet(workspace.CacheNamespaceWorkspaceTokens, tokenString); found {
			return nil, fmt.Errorf("token has been revoked")
		}
		if claims.AccountId != requestedAccountId {
			return nil, fmt.Errorf("token does not match account")
		}
		if claims.TenantId != "" && claims.TenantId != tenantId {
			return nil, fmt.Errorf("token does not match tenant")
		}
	} else {
		return nil, fmt.Errorf("invalid token claims")
	}

	return security.NewRequestContextForTenantAdmin(tenantId), nil
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

	var relayJob tools.RelayJob
	var registeredToolName string
	toolName := strings.ToLower(req.Tool)

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
	case "rabbitmq", "rabbitmqadmin":
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

	go func() {
		auditReq := &audit.AuditRequest{
			Audits: []audit.Audit{
				{
					AccountId:     req.AccountId,
					TenantId:      ctx.GetSecurityContext().GetTenantId(),
					EventTime:     time.Now(),
					EventCategory: audit.EventCategoryK8sRelay,
					EventType:     audit.EventTypeK8sRelayTask,
					EventActor:    audit.EventActorK8sAgent,
					EventAction:   audit.EventActionCreate,
					EventTarget:   toolName,
					EventStatus:   audit.EventStatusSuccess,
					EventState:    map[string]any{"command": req.Command},
					EventAttr: map[string]any{
						"command":   req.Command,
						"tool":      req.Tool,
						"arguments": req.Arguments,
						"source":    "workspace-shim",
					},
				},
			},
		}
		_ = audit.CreateAudit(ctx, auditReq)
	}()

	result, err := tools.ExecuteContainerJob(toolCtx, relayJob, req.Command, req.AccountId, req.Arguments, true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "result": result})
		return
	}

	c.JSON(http.StatusOK, gin.H{"result": result})
}
