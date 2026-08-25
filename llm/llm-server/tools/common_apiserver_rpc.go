package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// doApiServerActionRequest posts an action request to an api-server /rpc/*
// endpoint on behalf of the requesting user: X-ACTION-TOKEN authenticates the
// service hop, and x-tenant-id / x-user-id let api-server rebuild the user's
// own SecurityContext, so the RPC handler enforces the user's real role.
// x-user-id uses EffectiveUserIdForRPC — omitting it would silently escalate
// the call to tenant-admin on the api-server side.
func doApiServerActionRequest(nbCtx core.NbToolContext, rpcPath string, actionName string, input map[string]any, errPrefix string) (string, error) {
	tenant := ""
	if nbCtx.Ctx != nil {
		if sc := nbCtx.Ctx.GetSecurityContext(); sc != nil {
			tenant = sc.GetTenantId()
		}
	}
	if tenant == "" {
		if nbCtx.AccountId == "" {
			return "", fmt.Errorf("%s: account id is empty and tenant id is missing", errPrefix)
		}
		t, err := security.GetTenantIdFromAccountId(nbCtx.AccountId)
		if err != nil {
			return "", fmt.Errorf("%s: resolving tenant from account %s: %w", errPrefix, nbCtx.AccountId, err)
		}
		tenant = t
	}
	if tenant == "" {
		return "", fmt.Errorf("%s: tenant id is empty", errPrefix)
	}

	userId := ""
	if nbCtx.Ctx != nil {
		if sc := nbCtx.Ctx.GetSecurityContext(); sc != nil {
			userId = sc.EffectiveUserIdForRPC()
		}
	}
	if userId == "" {
		// EffectiveUserIdForRPC collapses the system-user sentinel to "" so
		// automated read flows can take api-server's tenant-admin branch. For
		// these mutating tools that branch would be a silent privilege
		// escalation — and a system context has no human to answer the write
		// confirmation — so fail closed: resolution writes run only on behalf
		// of a real requesting user.
		return "", fmt.Errorf("%s: %s requires a requesting user; refusing to run a write without one", errPrefix, actionName)
	}

	payload := map[string]any{
		"action": map[string]any{"name": actionName},
		"input":  input,
	}

	resp, err := common.HttpPost(
		fmt.Sprintf("%s%s", config.Config.ServiceEndpoint, rpcPath),
		common.HttpWithHeaders(map[string]string{
			"Content-Type":   "application/json",
			"Accept":         "application/json",
			"X-ACTION-TOKEN": config.Config.ServiceApiServerToken,
			"x-tenant-id":    tenant,
			"x-user-id":      userId,
		}),
		common.HttpWithJsonBody(payload),
	)
	if err != nil {
		return "", fmt.Errorf("%s: %s, unable to process request: %w", errPrefix, actionName, err)
	}
	defer func() {
		if resp != nil && resp.Body != nil {
			if cerr := resp.Body.Close(); cerr != nil {
				slog.Info(errPrefix+": failed to close response body", "error", cerr)
			}
		}
	}()

	jsonBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%s: %s, reading response body: %w", errPrefix, actionName, err)
	}

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return "", fmt.Errorf("%s: %s not permitted for this user: %s", errPrefix, actionName, string(jsonBody))
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s: %s failed (status %d): %s", errPrefix, actionName, resp.StatusCode, string(jsonBody))
	}

	trimmed := bytes.TrimLeft(jsonBody, " \t\r\n")
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return "", fmt.Errorf("%s: %s unexpected response shape: %s", errPrefix, actionName, string(jsonBody))
	}

	return string(jsonBody), nil
}

// perActionConfirmationKey builds the toolcore.ToolConfirmationScope key for
// the recommendation write tools: the tool name plus a short digest of the
// input, so every distinct invocation gets its own user confirmation while a
// resume of the same approved action maps back to its recorded approval.
func perActionConfirmationKey(toolName, toolInput string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(toolInput)))
	return toolName + ":" + hex.EncodeToString(sum[:])[:12]
}

// confirmationArgs parses a write tool's raw input for approval-card
// rendering. Card text must never break the confirmation itself, so a parse
// failure just yields an empty map and the caller falls back to the default
// rendering.
func confirmationArgs(toolInput string) map[string]any {
	args := map[string]any{}
	_ = json.Unmarshal([]byte(toolInput), &args)
	return args
}

// errNBLLMToolResponse surfaces a write-tool failure to the ReAct loop as a
// recoverable observation rather than an execution abort.
func errNBLLMToolResponse(err error) core.NBToolResponse {
	return core.NBToolResponse{
		Data:   fmt.Sprintf(`{"error": %q}`, err.Error()),
		Type:   core.NBToolResponseTypeJson,
		Status: core.NBToolResponseStatusError,
	}
}

var errRecommendationIdRequired = errors.New("recommendation_id is required")
