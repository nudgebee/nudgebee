package tools

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
)

// listIntegrationConfigs is replaced in tests, which have no integration store.
var listIntegrationConfigs = core.ListToolConfigs

// IsTargetedIntegrationTool reports a tool that follows a target and runs
// against one of the target account's own integrations (a database, a cache, a
// broker). Its integration is picked from the account the call reads, never
// from the conversation's account when the call is about another one.
func IsTargetedIntegrationTool(ctx *security.RequestContext, tool core.NBTool) bool {
	if tool == nil {
		return false
	}
	if _, byAccount := tool.(accountLevelIntegration); byAccount {
		return false
	}
	if decl, _ := core.AccountScopeOf(tool.Name()); decl.Scope != core.AccountScopeFollowsTarget {
		return false
	}
	configurable, ok := tool.(core.NBToolConfig)
	return ok && configurable.ConfigSchema(ctx).ConfigSource == core.ToolConfigSourceIntegration
}

// accountLevelIntegration marks an integration tool whose backend picks the
// integration by account (services-server's Datadog logs and traces): it
// follows a target account but never picks, names or switches a connection.
type accountLevelIntegration interface{ queriesIntegrationByAccount() }

// CallHasTarget reports a call that names a target in its input, or runs in a
// child handed another account. Such a call's integration is resolved on the
// target by ResolveToolCallTarget, so the conversation's own configs are not
// consulted for it.
func CallHasTarget(input string, delegated *core.ExecutionTarget, originAccountID string) bool {
	if target, err := core.ParseExecutionTarget(core.NBToolCallRequest{Command: input}); err == nil && target != nil {
		return true
	}
	return delegated != nil && delegated.AccountID != "" && delegated.AccountID != originAccountID
}

// targetIntegrationConfig picks the integration an integration tool's call
// runs against on its data account: the only one there, or the one the tool
// identifies from the call (its `instance`). Two integrations with the chosen
// name cannot be told apart downstream, where the workspace looks the
// integration up by name, so neither is used.
func targetIntegrationConfig(ctx core.NbToolContext, tool core.NBTool, request core.NBToolCallRequest) (core.ToolConfig, error) {
	label := "this conversation's account"
	if ctx.ExecutedTarget != nil {
		label = ctx.ExecutedTarget.Kind() + " " + ctx.ExecutedTarget.Name
	}
	configs, err := listIntegrationConfigs(ctx.Ctx, ctx.TargetAccountId, tool)
	if err != nil {
		return core.ToolConfig{}, fmt.Errorf("%s: listing integrations on %s: %w", tool.Name(), label, err)
	}
	if len(configs) == 0 {
		return core.ToolConfig{}, fmt.Errorf("%s has no integration for %s; it cannot be checked there", label, tool.Name())
	}
	chosen := core.ToolConfig{}
	instance := RequestedInstance(request)
	if instance == "" {
		// The user's pick, asked for by the planner for this account.
		instance = ctx.QueryConfig.ToolConfigs[TargetIntegrationChoiceKey(tool.Name(), ctx.TargetAccountId)]
	}
	if len(configs) == 1 {
		chosen = configs[0]
	} else if instance != "" {
		// Only the named instance picks among the target's integrations. An
		// exact name wins over a tool's fuzzy matching (a host regex can match
		// a different integration first); two integrations with that name are
		// refused below.
		for _, c := range configs {
			if strings.EqualFold(c.Name, instance) {
				chosen = c
				break
			}
		}
		// The tools' wider matching also reads names out of the call's text,
		// which carries the target's own name (k8s-prod would pick "prod").
		if identifier, ok := tool.(core.NBToolConfigIdentifier); ok && chosen.Name == "" {
			blank := ctx
			blank.ToolConfig = core.ToolConfig{}
			blank.Query = ""
			chosen, _ = identifier.IdentifyConfig(blank, core.NBToolCallRequest{Arguments: map[string]any{"instance": instance}}, configs)
		}
	}
	names := make([]string, 0, len(configs))
	same := 0
	for _, c := range configs {
		names = append(names, c.Name)
		if chosen.Name != "" && strings.EqualFold(c.Name, chosen.Name) {
			same++
		}
	}
	sort.Strings(names)
	if chosen.Name == "" && len(UniqueConfigNames(configs)) == 1 {
		return core.ToolConfig{}, fmt.Errorf("%s has %d integrations for %s, all named %q, which cannot be told apart, so none was used; rename one of them in Integrations", label, len(configs), tool.Name(), configs[0].Name)
	}
	if chosen.Name == "" {
		return core.ToolConfig{}, fmt.Errorf("%s has several integrations for %s (%s); name one with `instance`", label, tool.Name(), strings.Join(names, ", "))
	}
	if same > 1 {
		return core.ToolConfig{}, fmt.Errorf("%s has %d integrations named %q, which cannot be told apart, so none was used; rename one of them in Integrations", label, same, chosen.Name)
	}
	return chosen, nil
}

// IntegrationNamedByCall is the integration of the account the call reads
// that the call names with `instance`, when that is not the one the context
// already has. Only an exact (case-insensitive) name that one integration
// carries counts: models also put database or host names there, and those
// keep the conversation's pick.
func IntegrationNamedByCall(ctx core.NbToolContext, tool core.NBTool, request core.NBToolCallRequest) (core.ToolConfig, bool) {
	if strings.EqualFold(RequestedInstance(request), ctx.ToolConfig.Name) {
		return core.ToolConfig{}, false
	}
	configs, err := listIntegrationConfigs(ctx.Ctx, ctx.DataAccountID(), tool)
	if err != nil {
		return core.ToolConfig{}, false
	}
	return IntegrationNamedIn(configs, request)
}

// IntegrationNamedIn is the one config the call's `instance` names exactly
// (case-insensitively); none when the call names nothing, or a name no
// single config carries.
func IntegrationNamedIn(configs []core.ToolConfig, request core.NBToolCallRequest) (core.ToolConfig, bool) {
	instance := RequestedInstance(request)
	if instance == "" {
		return core.ToolConfig{}, false
	}
	var named []core.ToolConfig
	for _, c := range configs {
		if strings.EqualFold(c.Name, instance) {
			named = append(named, c)
		}
	}
	if len(named) != 1 {
		return core.ToolConfig{}, false
	}
	return named[0], true
}

// instanceProperty is the `instance` argument an integration tool takes to
// name the connection a call runs on.
var instanceProperty = core.ToolSchemaProperty{
	Type:        core.ToolSchemaTypeString,
	Description: "Name of the configured connection to run on, when several are configured (optional).",
}

// TargetIntegrationChoiceKey is where the user's pick among another account's
// integrations is kept in QueryConfig.ToolConfigs. The tool's own key holds the
// conversation account's pick; sharing it would make a pick on one account
// select a same-named integration on the other.
func TargetIntegrationChoiceKey(toolName, accountID string) string {
	return toolName + "@" + accountID
}

// UniqueConfigNames lists the distinct names of configs, sorted, for a picker.
func UniqueConfigNames(configs []core.ToolConfig) []string {
	seen := map[string]bool{}
	names := []string{}
	for _, c := range configs {
		if key := strings.ToLower(c.Name); !seen[key] {
			seen[key] = true
			names = append(names, c.Name)
		}
	}
	sort.Strings(names)
	return names
}

// RequestedInstance is the `instance` a call names, from its arguments or its
// JSON input.
func RequestedInstance(request core.NBToolCallRequest) string {
	if v, ok := request.Arguments["instance"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	var input map[string]any
	if json.Unmarshal([]byte(request.Command), &input) == nil {
		if v, ok := input["instance"].(string); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// IntegrationWorkspaceEnv is the workspace env of an integration tool's
// command: the integration's name and, for another account, that account, so
// the command's token is bound to it and the workspace proxy looks the
// integration up and relays there.
func IntegrationWorkspaceEnv(ctx core.NbToolContext, extra map[string]string) map[string]string {
	env := map[string]string{workspace.ENV_NB_TOOL_CONFIG_NAME: ctx.ToolConfig.Name}
	for k, v := range extra {
		env[k] = v
	}
	if account := ctx.DataAccountID(); account != "" && account != ctx.AccountId {
		env[workspace.ENV_NB_TARGET_ACCOUNT_ID] = account
	}
	return env
}

// IntegrationConfigByName is the integration named `name` on account, for the
// workspace proxy running a command bound to that account. A name shared by
// two of the account's integrations is refused: the command carries only the
// name, so either could be meant.
func IntegrationConfigByName(ctx *security.RequestContext, account string, tool core.NBTool, name string) (core.ToolConfig, error) {
	configs, err := listIntegrationConfigs(ctx, account, tool)
	if err != nil {
		return core.ToolConfig{}, fmt.Errorf("listing integrations: %w", err)
	}
	var found []core.ToolConfig
	for _, c := range configs {
		if strings.EqualFold(c.Name, name) {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return core.ToolConfig{}, fmt.Errorf("integration %q is not connected to the selected account", name)
	default:
		return core.ToolConfig{}, fmt.Errorf("%d integrations named %q are connected to the selected account; none was used", len(found), name)
	}
}
