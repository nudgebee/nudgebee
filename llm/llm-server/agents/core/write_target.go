package core

import (
	"fmt"
	"strings"

	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// writeTarget is where a write lands when that is not the conversation's own
// account: the cluster or account the call names or was handed, and for an
// integration tool the integration picked there.
type writeTarget struct {
	Target      toolcore.ExecutionTarget
	Integration string
}

// writeTargetOf resolves, with the rules the call itself runs by, where a
// write lands. Nil means the conversation's own account, or a target that
// does not resolve (the call is then refused on its own path). Every place
// that approves or authorizes a write uses it, so the account the user
// approves is the account the key, the card and the access check name.
func (e *plannerExecutor) writeTargetOf(tool toolcore.NBTool, action NBAgentPlannerToolAction) *writeTarget {
	if tool == nil {
		return nil
	}
	origin := e.agentRequest.AccountId
	fromConfig := func(owner string, selected toolcore.ToolConfig) *writeTarget {
		account := tools.TargetAccountID(selected)
		if account == "" || account == origin {
			return nil
		}
		return &writeTarget{Target: toolcore.ExecutionTarget{Provider: tools.ProviderOfTool(owner), Name: selected.Name, AccountID: account}}
	}

	// A shell command lands where its CLI's --context/--profile/account says.
	if strings.EqualFold(tool.Name(), toolcore.ToolExecuteShellCommand) {
		owner := configOwnerForAction(tool, origin, action.ToolInput)
		if owner == nil || owner.Name() == tool.Name() {
			return nil
		}
		selected, err := e.shellWriteTarget(owner, action)
		if err != nil {
			return nil
		}
		return fromConfig(owner.Name(), selected)
	}

	// kubectl, helm and the cloud CLIs name their cluster or account.
	if scoped, ok := tool.(toolcore.PerCallTargetTool); ok {
		selected, found, err := e.perCallWriteTarget(tool, scoped, action)
		if err != nil || !found {
			return nil
		}
		return fromConfig(tool.Name(), selected)
	}

	// Tools that follow a target (databases, VMs, …): the call's `target`,
	// else the one handed to this agent.
	if decl, _ := toolcore.AccountScopeOf(tool.Name()); decl.Scope != toolcore.AccountScopeFollowsTarget {
		return nil
	}
	request := toolcore.ParseNBToolCallRequestFromInput(action.ToolInput)
	callCtx := toolcore.NbToolContext{Ctx: e.ctx, AccountId: origin, UserId: e.agentRequest.UserId, QueryConfig: e.agentRequest.QueryConfig}
	resolved, err := tools.ResolveCallTarget(callCtx, request)
	if err != nil || resolved.ExecutedTarget == nil {
		return nil
	}
	target := &writeTarget{Target: *resolved.ExecutedTarget}
	if tools.IsTargetedIntegrationTool(e.ctx, tool) {
		// The integration is part of where the write lands. It may still
		// need the user's pick; the account alone then scopes the approval.
		if picked, pickErr := tools.ResolveToolCallTarget(callCtx, tool, request); pickErr == nil {
			target.Integration = picked.ToolConfig.Name
		}
	}
	return target
}

// confirmationKeyWithTarget scopes a write approval to the account it lands
// on, by id, so a replaced connection is asked again. As on the conversation's
// own account, one approval covers the tool's writes there; the integration
// is not part of the key, because it may be picked only after the approval.
// A write on the conversation's own account keeps its key unchanged.
func confirmationKeyWithTarget(key string, target *writeTarget) string {
	if target == nil {
		return key
	}
	return key + "@" + target.Target.AccountID
}

// where names a write's destination for the approval card.
func (t *writeTarget) where() string {
	where := fmt.Sprintf("%s **%s**", t.Target.Kind(), t.Target.Name)
	if t.Integration != "" {
		where += " · " + t.Integration
	}
	return where
}

// confirmationQuestionWithTarget makes the approval card say where a write
// lands. Without a destination it keeps the generic text.
func confirmationQuestionWithTarget(question string, action NBAgentPlannerToolAction, requestType toolcore.ToolRequestType, target *writeTarget) string {
	if target == nil {
		return question
	}
	if question != "" {
		return fmt.Sprintf("On %s:\n%s", target.where(), question)
	}
	return fmt.Sprintf("Tool(%s) is trying to %s resources on %s. Do you want to continue?\nCommand - %s", action.Tool, requestType, target.where(), confirmationDisplayInput(action.ToolInput))
}

// destinationOf is where a write lands, for the approval card: the other
// account when it lands elsewhere, else the conversation's own account and
// the integration the call runs with there (the one it names, else the one
// already chosen: picked, or the only one). Nil when
// the conversation's account cannot be named; the card then keeps its old text.
func (e *plannerExecutor) destinationOf(tool toolcore.NBTool, action NBAgentPlannerToolAction, target *writeTarget) *writeTarget {
	if target != nil {
		return target
	}
	own := tools.AccountTarget(e.agentRequest.AccountId)
	if own == nil || own.Name == "" {
		return nil
	}
	destination := &writeTarget{Target: *own}
	if _, configurable := tool.(toolcore.NBToolConfig); configurable {
		name := e.resolvedToolConfig(tool.Name())
		if name == "" {
			if configs, err := toolcore.ListToolConfigs(e.ctx, e.agentRequest.AccountId, tool); err == nil && len(configs) == 1 {
				name = configs[0].Name
			}
		}
		// A call that names another of the account's integrations runs there.
		if tools.IsTargetedIntegrationTool(e.ctx, tool) {
			callCtx := toolcore.NbToolContext{Ctx: e.ctx, AccountId: e.agentRequest.AccountId, ToolConfig: toolcore.ToolConfig{Name: name}}
			if named, ok := tools.IntegrationNamedByCall(callCtx, tool, toolcore.ParseNBToolCallRequestFromInput(action.ToolInput)); ok {
				name = named.Name
			}
		}
		// A cluster's kubectl config is named after the cluster; placeholders
		// name no integration.
		if name != "" && !strings.EqualFold(name, own.Name) && name != toolcore.ToolConfigNameNone && name != toolcore.ToolConfigNameAccountAgent {
			destination.Integration = name
		}
	}
	return destination
}

// followupTarget is the destination as the approval card's data, for the UI.
func (t *writeTarget) followupTarget() *FollowupTarget {
	if t == nil {
		return nil
	}
	return &FollowupTarget{Provider: t.Target.Provider, Kind: t.Target.Kind(), Name: t.Target.Name, Integration: t.Integration}
}

// confirmationKeyOf is the key the gate records an approval under, for a
// resume that must look it up under the same key.
func (e *plannerExecutor) confirmationKeyOf(nameToTool map[string]toolcore.NBTool, action NBAgentPlannerToolAction) string {
	tool, ok := lookupToolForPlanner(nameToTool, e.agentRequest.AccountId, action.Tool)
	if !ok || tool == nil {
		return action.Tool
	}
	return e.approvalKey(tool, action, e.writeTargetOf(tool, action))
}

// approvalKey is the key a write's approval is recorded and looked up under:
// the tool's key, scoped to the account the write lands on, and on the
// conversation's own account to the integration a call switches to, so a
// "yes" given for one database does not run a write the call aimed at
// another. A write that does not switch keeps its key.
func (e *plannerExecutor) approvalKey(tool toolcore.NBTool, action NBAgentPlannerToolAction, target *writeTarget) string {
	key := confirmationKeyWithTarget(confirmationKeyForAction(tool, action), target)
	if target == nil && e.namesWhereItRunsAnyway(tool, action, key) {
		// "kubectl --context k8s-dev" in a k8s-dev conversation is the same
		// write as without it; one approval covers both.
		return action.Tool
	}
	if target == nil && tools.IsTargetedIntegrationTool(e.ctx, tool) {
		callCtx := toolcore.NbToolContext{Ctx: e.ctx, AccountId: e.agentRequest.AccountId, ToolConfig: toolcore.ToolConfig{Name: e.resolvedToolConfig(tool.Name())}}
		if named, ok := tools.IntegrationNamedByCall(callCtx, tool, toolcore.ParseNBToolCallRequestFromInput(action.ToolInput)); ok {
			key += "#" + named.Name
		}
	}
	return key
}

// namesWhereItRunsAnyway reports whether a per-call tool's key only names the
// cluster or account the call would run on without naming it: the
// conversation's chosen config, or its only one. Naming another of the
// account's configs keeps its own key.
func (e *plannerExecutor) namesWhereItRunsAnyway(tool toolcore.NBTool, action NBAgentPlannerToolAction, key string) bool {
	scoped, ok := tool.(toolcore.PerCallTargetTool)
	if !ok {
		return false
	}
	requested, err := scoped.RequestedTarget(action.ToolInput)
	if err != nil || requested == "" || !strings.EqualFold(key, tool.Name()+"@"+requested) {
		return false
	}
	selected, found, err := e.perCallWriteTarget(tool, scoped, action)
	if err != nil || !found || selected.Name == "" {
		return false
	}
	unnamed := e.resolvedToolConfig(tool.Name())
	if unnamed == "" {
		if configs, err := toolcore.ListToolConfigs(e.ctx, e.agentRequest.AccountId, tool); err == nil && len(configs) == 1 {
			unnamed = configs[0].Name
		}
	}
	return strings.EqualFold(selected.Name, unnamed)
}

// writeAccountOf is the account a write must be allowed on: where it lands
// when that is another account and the target-write rule is on, else the
// conversation's.
func writeAccountOf(origin string, target *writeTarget, targetRule bool) []string {
	if target == nil {
		return []string{origin}
	}
	if targetRule {
		return []string{target.Target.AccountID}
	}
	return []string{origin, target.Target.AccountID}
}
