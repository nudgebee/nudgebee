package tools

import (
	"fmt"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/hashicorp/golang-lru/v2/expirable"
)

// ResolveAgentHandoffTarget validates a child agent's named connection. The
// execution tool still resolves and authorizes its concrete call separately.
func ResolveAgentHandoffTarget(ctx *security.RequestContext, accountID string, target *core.ExecutionTarget) (*core.ExecutionTarget, error) {
	if target == nil {
		return nil, nil
	}
	if ctx == nil || ctx.GetSecurityContext() == nil {
		return nil, fmt.Errorf("caller security context is required")
	}
	var selected core.ToolConfig
	var err error
	switch target.Provider {
	case "kubernetes":
		selected, err = KubernetesTargetByName(ctx, accountID, target.Name)
	case "aws":
		selected, err = AccountTargetByName(ctx, accountID, ToolExecuteAwsCliCommand, target.Name)
	case "gcp":
		selected, err = AccountTargetByName(ctx, accountID, ToolExecuteGcpCliCommand, target.Name)
	case "azure":
		selected, err = AccountTargetByName(ctx, accountID, ToolExecuteAzureCliCommand, target.Name)
	case "vm":
		selected, err = VMAccountTargetByName(ctx, accountID, target.Name)
	default:
		return nil, fmt.Errorf("unsupported provider %q", target.Provider)
	}
	if err != nil {
		return nil, err
	}
	targetAccountID := TargetAccountID(selected)
	if targetAccountID == "" {
		return nil, fmt.Errorf("%s target %q has no account id", target.Provider, selected.Name)
	}
	return &core.ExecutionTarget{Provider: target.Provider, Name: selected.Name, AccountID: targetAccountID}, nil
}

// VerifyDelegatedTargetIdentity prevents a renamed/replaced connection from
// changing where a child runs after its target was validated or approved.
func VerifyDelegatedTargetIdentity(ctx core.NbToolContext, provider string, selected core.ToolConfig, requested string) error {
	bound := ctx.QueryConfig.DelegatedTarget
	if bound == nil || bound.AccountID == "" || bound.Provider != provider ||
		(!strings.EqualFold(requested, bound.Name) && !strings.EqualFold(selected.Name, bound.Name)) {
		return nil
	}
	if actual := TargetAccountID(selected); actual != bound.AccountID {
		return fmt.Errorf("delegated %s target %q changed since handoff; select the connection again", provider, bound.Name)
	}
	return nil
}

// ResolveObservabilityTarget resolves and validates a target for an observability tool
// (metrics, traces, logs). If an explicit target is provided in input, it resolves and
// verifies that target. Otherwise, if ctx.QueryConfig.DelegatedTarget is bound, it
// uses that bound target. If no target is present, it returns nil, ctx.AccountId, nil.
func ResolveObservabilityTarget(ctx core.NbToolContext, input core.NBToolCallRequest) (*core.ExecutionTarget, string, error) {
	target, err := core.ParseExecutionTarget(input)
	if err != nil {
		return nil, "", fmt.Errorf("target: %w", err)
	}
	if target == nil {
		target = ctx.QueryConfig.DelegatedTarget
	}
	if target == nil {
		return nil, ctx.AccountId, nil
	}
	if ctx.Ctx == nil && target.AccountID == "" {
		return nil, "", fmt.Errorf("observability target %s/%s: caller security context is required to resolve target", target.Provider, target.Name)
	}
	// If the target already has an AccountID and caller request context is nil (common in unit tests),
	// use it directly.
	if target.AccountID != "" && ctx.Ctx == nil {
		return target, target.AccountID, nil
	}
	resolved, err := ResolveAgentHandoffTarget(ctx.Ctx, ctx.AccountId, target)
	if err != nil {
		return nil, "", fmt.Errorf("observability target %s/%s: %w", target.Provider, target.Name, err)
	}
	if target.AccountID != "" && resolved.AccountID != target.AccountID {
		return nil, "", fmt.Errorf("delegated %s target %q changed since handoff; select the connection again", target.Provider, target.Name)
	}
	return resolved, resolved.AccountID, nil
}

// ResolveCallTarget sets ctx.TargetAccountId for one tool call: the call's
// explicit "target" argument, else the target handed to the agent, else the
// conversation's own account. The caller must be able to read the target, and
// a handed target must still be the connection validated at handoff.
func ResolveCallTarget(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NbToolContext, error) {
	target, accountID, err := ResolveObservabilityTarget(ctx, input)
	if err != nil {
		return ctx, err
	}
	ctx.TargetAccountId = accountID
	if target != nil && accountID != ctx.AccountId {
		ctx.ExecutedTarget = &core.ExecutionTarget{Provider: target.Provider, Name: target.Name, AccountID: accountID}
	}
	return ctx, nil
}

// ExecutedTargetOf reports where a tool call ran when that is not the
// conversation's default: the resolved target of a follows-target tool, the
// cluster/account an own-selector tool was asked for (it validates the name
// and fails otherwise), or the target an agent called as a tool was given.
func ExecutedTargetOf(tool core.NBTool, ctx core.NbToolContext, input string) *core.ExecutionTarget {
	if ctx.ExecutedTarget != nil {
		return ctx.ExecutedTarget
	}
	owner, requested := "", ""
	if strings.EqualFold(tool.Name(), core.ToolExecuteShellCommand) {
		owner, requested, _ = RequestedShellTargetFromInput(input)
	} else if scoped, ok := tool.(core.PerCallTargetTool); ok {
		owner = tool.Name()
		requested, _ = scoped.RequestedTarget(input)
	}
	provider := providerOfTool(owner)
	if requested == "" || provider == "" {
		// Agents and sub-agent tools (metrics, prometheus, delegate) take
		// their target in the input and resolve it themselves.
		target, err := core.ParseExecutionTarget(core.NBToolCallRequest{Command: input})
		if err != nil {
			return nil
		}
		return target
	}
	if strings.EqualFold(requested, ctx.ToolConfig.Name) {
		return nil // named the conversation's own connection
	}
	return &core.ExecutionTarget{Provider: provider, Name: requested}
}

// StampExecutedTarget records on a call where it ran and with which
// integration, so the conversation view can show both. A call is stamped with
// the account it ran on whenever that is not the conversation's own, whether
// it succeeded or failed: a failure there is still an answer about that
// account. Calls that never reached an account (an unknown cluster, a refused
// target) and calls still waiting on the user are not stamped.
func StampExecutedTarget(resp core.NBToolResponse, callErr error, tool core.NBTool, ctx core.NbToolContext, input string) core.NBToolResponse {
	if callErr == nil && resp.Status == core.NBToolResponseStatusWaiting {
		return resp
	}
	origin := conversationAccountOf(ctx)
	ranOn := origin
	if recorded, ok := resp.AdditionalDetails[core.MetadataExecutedTarget].(map[string]any); ok {
		if id, _ := recorded["account_id"].(string); id != "" {
			ranOn = id
		}
	} else if target, known := executedTarget(tool, ctx, input, origin); !known {
		return resp // named somewhere that is not connected: it ran nowhere
	} else if target != nil {
		resp.AdditionalDetails = core.WithExecutedTarget(resp.AdditionalDetails, target)
		ranOn = target.AccountID
	}
	if name := usedIntegration(tool, ctx, ranOn); name != "" {
		if resp.AdditionalDetails == nil {
			resp.AdditionalDetails = map[string]any{}
		}
		resp.AdditionalDetails[core.MetadataResolvedTarget] = name
	}
	return resp
}

// executedTarget is the account a call ran on, when not origin (the
// conversation's account). A call names it, or was handed it; or it runs in a
// sub-agent started as that account (metrics, traces), or was pointed at it by
// its agent (logs), and then carries no target of its own. known is false
// when the call named a connection that does not resolve.
func executedTarget(tool core.NBTool, ctx core.NbToolContext, input, origin string) (target *core.ExecutionTarget, known bool) {
	if target := ExecutedTargetOf(tool, ctx, input); target != nil {
		if target.AccountID == "" {
			// An own-selector tool names a connection; the conversation's own
			// cluster may be named even when its tool config carries no name.
			resolved, err := resolveStampTarget(ctx.Ctx, ctx.AccountId, target)
			if err != nil {
				return nil, false
			}
			target = resolved
		}
		if target.AccountID == origin {
			return nil, true
		}
		return target, true
	}
	// The account the call runs under, which its record is saved with. Not
	// DataAccountID: a delegated child's origin-bound tools (workspace files)
	// carry the child's target there but run on the conversation's account.
	if ctx.AccountId == "" || ctx.AccountId == origin {
		return nil, true
	}
	return describeAccount(ctx.AccountId), true
}

// usedIntegration is the integration a call ran with, or "" when that is not
// known: the context's config was listed on the call's own account, or picked
// on the target for an integration tool, and names the integration only when
// that is the account the call ran on. A config of the conversation's account
// is not what a call on another cluster ran with, and placeholders name none.
func usedIntegration(tool core.NBTool, ctx core.NbToolContext, ranOn string) string {
	name := ctx.ToolConfig.Name
	if name == "" || name == core.ToolConfigNameNone || name == core.ToolConfigNameAccountAgent {
		return ""
	}
	listedOn := ctx.AccountId
	if IsTargetedIntegrationTool(ctx.Ctx, tool) {
		listedOn = ctx.DataAccountID()
	}
	if listedOn != ranOn {
		return ""
	}
	return name
}

// resolveStampTarget, conversationAccountOf and describeAccount are replaced in
// tests, which have no connection store.
var (
	resolveStampTarget    = ResolveAgentHandoffTarget
	conversationAccountOf = lookupConversationAccount
	describeAccount       = lookupAccountTarget
)

// A conversation's account never changes, so it is looked up once. Every call
// of a sub-agent running as another account names it, so names are cached too.
var (
	conversationAccounts, _ = lru.New[string, string](4096)
	accountTargets          = expirable.NewLRU[string, core.ExecutionTarget](1024, nil, 10*time.Minute)
)

// lookupConversationAccount is the account the call's conversation belongs
// to. A sub-agent may run as another account, so the context's own AccountId
// is the fallback only when the conversation cannot be read.
func lookupConversationAccount(ctx core.NbToolContext) string {
	if ctx.ConversationId == "" {
		return ctx.AccountId
	}
	if account, ok := conversationAccounts.Get(ctx.ConversationId); ok {
		return account
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return ctx.AccountId
	}
	row, err := dbms.QueryRow("select account_id::text from llm_conversations where id = $1", ctx.ConversationId)
	if err != nil {
		return ctx.AccountId
	}
	var account string
	if err := row.Scan(&account); err != nil || account == "" {
		return ctx.AccountId
	}
	conversationAccounts.Add(ctx.ConversationId, account)
	return account
}

// lookupAccountTarget names a connected account as a target, for a call that
// ran on it without naming it.
func lookupAccountTarget(accountID string) *core.ExecutionTarget {
	if cached, ok := accountTargets.Get(accountID); ok {
		return &cached
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil
	}
	row, err := dbms.QueryRow("select coalesce(account_name, ''), coalesce(cloud_provider, ''), coalesce(account_type, '') from cloud_accounts where id = $1", accountID)
	if err != nil {
		return nil
	}
	var name, cloudProvider, accountType string
	if err := row.Scan(&name, &cloudProvider, &accountType); err != nil {
		return nil
	}
	target := core.ExecutionTarget{Provider: core.AccountTargetProvider(cloudProvider, accountType), Name: name, AccountID: accountID}
	accountTargets.Add(accountID, target)
	return &target
}

// AccountTarget names a connected account as a target, e.g. to say on an
// approval card which account a write lands on. Nil when it cannot be read.
func AccountTarget(accountID string) *core.ExecutionTarget { return describeAccount(accountID) }

// ProviderOfTool is the target provider ("kubernetes", "aws", …) whose
// connections a CLI tool selects, or "" for a tool that selects none.
func ProviderOfTool(name string) string { return providerOfTool(name) }

func providerOfTool(name string) string {
	switch name {
	case ToolExecuteKubectlCommand, ToolExecuteHelmCommand, ToolResourceSearch:
		return "kubernetes"
	case ToolExecuteAwsCliCommand:
		return "aws"
	case ToolExecuteGcpCliCommand:
		return "gcp"
	case ToolExecuteAzureCliCommand:
		return "azure"
	}
	return ""
}

// ResolveToolCallTarget decides, in one place, which account a tool call
// reads. Every dispatch path uses it: the planner executor, custom planners
// (core.CallTool) and watch polls.
//
// A follows-target tool gets TargetAccountId from the call's explicit target,
// else the target handed to the agent, after the caller's access to it is
// checked. Agent-tools and own-selector tools route a target themselves. Any
// other tool given a target argument it does not define is refused rather
// than silently answering for the conversation's own account.
func ResolveToolCallTarget(ctx core.NbToolContext, tool core.NBTool, request core.NBToolCallRequest) (core.NbToolContext, error) {
	if _, agentTool := tool.(core.DelegatedTargetSupport); agentTool {
		return ctx, nil
	}
	if _, ownSelector := tool.(core.DelegatedTargetInputPreparer); ownSelector {
		return ctx, nil
	}
	decl, _ := core.AccountScopeOf(tool.Name())
	switch decl.Scope {
	case core.AccountScopeFollowsTarget:
		resolved, err := ResolveCallTarget(ctx, request)
		if err != nil || !IsTargetedIntegrationTool(ctx.Ctx, tool) {
			return resolved, err
		}
		// The conversation's own integration was picked when the context was
		// built; a call that names another of its integrations runs there
		// (#38596). A call on another account, or one that named its target
		// without a default, picks among the target's integrations instead.
		if resolved.TargetAccountId == resolved.AccountId && resolved.ToolConfig.Name != "" {
			if named, ok := IntegrationNamedByCall(resolved, tool, request); ok {
				resolved.ToolConfig = named
			}
			return resolved, nil
		}
		selected, err := targetIntegrationConfig(resolved, tool, request)
		if err != nil {
			return ctx, err
		}
		resolved.ToolConfig = selected
		return resolved, nil
	case core.AccountScopeIndependent:
		return ctx, nil
	}
	if raw, given := request.Arguments["target"]; given && raw != nil {
		if _, own := tool.InputSchema().Properties["target"]; !own {
			return ctx, fmt.Errorf("%s cannot run on another cluster or account: it only covers this conversation's own account", tool.Name())
		}
	}
	return ctx, nil
}
