package workspace

import (
	"fmt"
	"maps"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// AudienceWorkspacePod marks the pod's own long-lived token. It authenticates
	// the pod (file and LLM calls) and must not authorize tool execution.
	AudienceWorkspacePod = "workspace-pod"
	// AudienceWorkspaceExecute marks a per-command execution token. The shim
	// sends it to /api/v1/workspace/execute, which authorizes the call as the
	// user in its grants.
	AudienceWorkspaceExecute = "workspace-execute"

	// ENV_NB_TARGET_ACCOUNT_ID asks ExecuteCommand to bind the command's
	// execution token to another connected account (a per-call cluster). It is
	// consumed by ExecuteCommand and never reaches the pod.
	ENV_NB_TARGET_ACCOUNT_ID = "NB_TARGET_ACCOUNT_ID"

	// executionTokenGrace keeps a token valid a little past the HTTP timeout
	// of the call that carries its command, so the command's last shim call
	// is never rejected as expired.
	executionTokenGrace = 30 * time.Second
)

// ExecutionToken signs a short-lived token for one command in the workspace
// of workspaceAccount, carrying the caller's grants. A non-empty
// targetAccount binds the token to that account, which the caller must be
// able to read.
func ExecutionToken(ctx *security.RequestContext, workspaceAccount, targetAccount string, ttl time.Duration) (string, error) {
	tenantId, err := security.GetTenantIdFromAccountId(workspaceAccount)
	if err != nil {
		return "", fmt.Errorf("execution token: tenant lookup: %w", err)
	}
	var sc *security.SecurityContext
	if ctx != nil {
		sc = ctx.GetSecurityContext()
	}
	if targetAccount != "" && (sc == nil || !sc.HasAccountAccess(targetAccount, security.SecurityAccessTypeRead)) {
		return "", fmt.Errorf("target cluster access denied")
	}
	grants, err := security.WorkspaceGrantsFor(sc, tenantId)
	if err != nil {
		return "", fmt.Errorf("execution token: %w", err)
	}
	now := time.Now()
	claims := WorkspaceTokenClaims{
		AccountId:       workspaceAccount,
		TenantId:        tenantId,
		TargetAccountId: targetAccount,
		Grants:          &grants,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{AudienceWorkspaceExecute},
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "llm-server",
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(config.Config.LlmServerJwtSecret))
	if err != nil {
		return "", fmt.Errorf("execution token: sign: %w", err)
	}
	return token, nil
}

// commandEnv returns the env for one command attempt without changing the
// caller's map, so a retry after workspace recovery gets a fresh token. The
// execution token is always minted here: callers cannot supply their own.
func (w *workspaceManager) commandEnv(ctx *security.RequestContext, accountId string, env map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(env)+3)
	maps.Copy(out, env)
	target := out[ENV_NB_TARGET_ACCOUNT_ID]
	delete(out, ENV_NB_TARGET_ACCOUNT_ID)
	out["nb_account_id"] = accountId
	if ctx != nil && ctx.GetSecurityContext() != nil {
		out["nb_tenant_id"] = ctx.GetSecurityContext().GetTenantId()
	}
	token, err := ExecutionToken(ctx, accountId, target, w.httpClient.Timeout+executionTokenGrace)
	if err != nil {
		return nil, fmt.Errorf("workspace: %w", err)
	}
	out[ENV_NB_WORKSPACE_TOKEN] = token
	return out, nil
}
