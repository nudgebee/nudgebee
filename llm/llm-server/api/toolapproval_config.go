package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/security/toolapproval"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

type toolApprovalUpsertRequest struct {
	ID             string                   `json:"id"`
	AccountID      string                   `json:"account_id"`
	Name           string                   `json:"name"`
	Enabled        *bool                    `json:"enabled"`
	ToolName       string                   `json:"tool_name"`
	ToolConfigID   *string                  `json:"tool_config_id"`
	MatchKind      toolapproval.MatchKind   `json:"match_kind"`
	CommandPattern *string                  `json:"command_pattern"`
	WorkingDir     *string                  `json:"working_dir"`
	Conditions     []toolapproval.Condition `json:"conditions"`
	Revision       int                      `json:"revision"`
	// AllAccounts must be set explicitly for a rule without an account, so a
	// missing account_id is rejected rather than read as "every account".
	AllAccounts bool `json:"all_accounts"`
	// AllowUnattended lets the rule apply in runs with no signed-in person.
	AllowUnattended bool `json:"allow_unattended"`
}

type toolApprovalCreateRequest struct {
	Rule     toolApprovalUpsertRequest   `json:"rule"`
	Accounts []toolApprovalCreateAccount `json:"accounts"`
	// Integrations pins an organisation rule to these integrations, one rule
	// each; empty means one rule for every integration.
	Integrations []string `json:"integrations"`
}

type toolApprovalCreateAccount struct {
	AccountID    string  `json:"account_id"`
	ToolConfigID *string `json:"tool_config_id"`
}

type toolApprovalDeleteRequest struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
}

type toolApprovalCheckRequest struct {
	Rule  toolApprovalUpsertRequest `json:"rule"`
	Input string                    `json:"input"`
}

type toolApprovalOptionsRequest struct {
	AccountIDs     []string `json:"account_ids"`
	ToolName       string   `json:"tool_name"`
	MatchKind      string   `json:"match_kind"`
	CommandPattern string   `json:"command_pattern"`
	// Organisation asks for the tenant's integrations of the tool, which an
	// organisation rule can be pinned to.
	Organisation bool `json:"organisation"`
}

type toolApprovalConfigOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type toolApprovalToolOption struct {
	Name              string `json:"name"`
	ConfigurationMode string `json:"configuration_mode"`
	// TargetKind says what a call of this tool runs against, which decides how
	// a rule is scoped: "account" (a cluster or cloud account), "integration",
	// "command" (decided by the shell command) or "none".
	TargetKind string `json:"target_kind"`
	// Fields are the tool's input fields an argument condition can test.
	Fields []toolApprovalToolField `json:"fields"`
	// Source is "mcp" for an MCP server's tool, whose calls have no command.
	Source string `json:"source,omitempty"`
}

type toolApprovalToolField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Enum        []any  `json:"enum,omitempty"`
	// Command marks a field carrying a CLI command; a condition on it checks
	// each command with the same parser as the command rules.
	Command bool `json:"command,omitempty"`
}

// toolApprovalFields lists a tool's input fields that argument conditions can
// test, sorted by name, marking the ones that carry CLI commands.
func toolApprovalFields(tool toolcore.NBTool) []toolApprovalToolField {
	schema := tool.InputSchema()
	fields := make([]toolApprovalToolField, 0, len(schema.Properties))
	for name, property := range schema.Properties {
		fields = append(fields, toolApprovalToolField{Name: name, Type: string(property.Type), Description: property.Description, Enum: property.Enum, Command: toolapproval.IsExecutableField(name)})
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	return fields
}

const (
	toolApprovalTargetAccount     = "account"
	toolApprovalTargetIntegration = "integration"
	toolApprovalTargetCommand     = "command"
	toolApprovalTargetNone        = "none"
)

// toolApprovalTargetKind classifies what a tool's calls run against and, for
// integration tools, which integration types they use.
func toolApprovalTargetKind(ctx *security.RequestContext, tool toolcore.NBTool) (string, []string) {
	if strings.EqualFold(tool.Name(), toolcore.ToolExecuteShellCommand) {
		return toolApprovalTargetCommand, nil
	}
	configurable, ok := tool.(toolcore.NBToolConfig)
	if !ok {
		return toolApprovalTargetNone, nil
	}
	schema := configurable.ConfigSchema(ctx)
	switch schema.ConfigSource {
	case toolcore.ToolConfigSourceIntegration, toolcore.ToolConfigSourceTicket:
		if schema.ConfigType != "" {
			return toolApprovalTargetIntegration, []string{schema.ConfigType}
		}
	}
	return toolApprovalTargetAccount, nil
}

type toolApprovalAccountOptions struct {
	AccountID             string                     `json:"account_id"`
	ConfigurationRequired bool                       `json:"configuration_required"`
	ConfigurationPending  bool                       `json:"configuration_pending"`
	Configurations        []toolApprovalConfigOption `json:"configurations"`
	Error                 string                     `json:"error,omitempty"`
}

func requireToolApprovalTenantAdmin(c *gin.Context, ctx *security.RequestContext) bool {
	if ctx == nil || ctx.GetSecurityContext() == nil || (!ctx.GetSecurityContext().IsTenantAdmin() && !ctx.GetSecurityContext().IsSuperAdmin()) {
		c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New("tool approval: tenant administrator access is required")}))
		return false
	}
	return true
}

func requireToolApprovalTenantReader(c *gin.Context, ctx *security.RequestContext) bool {
	if ctx == nil || ctx.GetSecurityContext() == nil {
		c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New("tool approval: tenant administrator access is required")}))
		return false
	}
	securityContext := ctx.GetSecurityContext()
	if !securityContext.IsTenantAdmin() && !securityContext.IsTenantReadAdmin() && !securityContext.IsSuperAdmin() && !securityContext.IsSuperAdminReadonly() {
		c.JSON(http.StatusForbidden, buildApiResponse(nil, []error{errors.New("tool approval: tenant administrator access is required")}))
		return false
	}
	return true
}

func toolApprovalTenantID(ctx *security.RequestContext) (uuid.UUID, error) {
	if ctx == nil || ctx.GetSecurityContext() == nil {
		return uuid.Nil, errors.New("tool approval: unable to identify tenant")
	}
	id, err := uuid.Parse(ctx.GetSecurityContext().GetTenantId())
	if err != nil {
		return uuid.Nil, errors.New("tool approval: unable to identify tenant")
	}
	return id, nil
}

func decodeToolApprovalPayload(payload map[string]any, target any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

func ruleFromRequest(request toolApprovalUpsertRequest, tenantID uuid.UUID) (*toolapproval.Rule, error) {
	var accountID *uuid.UUID
	var err error
	rawAccountID := strings.TrimSpace(request.AccountID)
	switch {
	case request.AllAccounts && rawAccountID != "":
		return nil, errors.New("tool approval: choose either all accounts or one account")
	case !request.AllAccounts:
		parsed, parseErr := uuid.Parse(rawAccountID)
		if parseErr != nil {
			return nil, errors.New("tool approval: valid account_id is required unless all_accounts is set")
		}
		accountID = &parsed
	}
	id := uuid.Nil
	if strings.TrimSpace(request.ID) != "" {
		id, err = uuid.Parse(request.ID)
		if err != nil {
			return nil, errors.New("tool approval: invalid rule id")
		}
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	requestConditions := request.Conditions
	if requestConditions == nil {
		// An omitted list must be stored as [] rather than JSON null, which the
		// table's match-shape constraint rejects.
		requestConditions = []toolapproval.Condition{}
	}
	conditions, err := json.Marshal(requestConditions)
	if err != nil {
		return nil, err
	}
	var toolConfigID *string
	if request.ToolConfigID != nil {
		trimmed := strings.TrimSpace(*request.ToolConfigID)
		if trimmed != "" {
			toolConfigID = &trimmed
		}
	}
	return &toolapproval.Rule{
		ID: id, TenantID: tenantID, AccountID: accountID,
		Name: strings.TrimSpace(request.Name), Enabled: enabled,
		ToolName: strings.TrimSpace(request.ToolName), MatchKind: request.MatchKind,
		ToolConfigID: toolConfigID, CommandPattern: request.CommandPattern,
		WorkingDir: request.WorkingDir, Conditions: conditions, Revision: request.Revision,
		AllowUnattended: request.AllowUnattended,
	}, nil
}

// buildToolApprovalRule parses a rule request and checks it against the
// caller's tenant: the account (when set) must belong to the tenant, and the
// tool and execution target must exist there.
func buildToolApprovalRule(ctx *security.RequestContext, request toolApprovalUpsertRequest, tenantID uuid.UUID) (*toolapproval.Rule, error) {
	rule, err := ruleFromRequest(request, tenantID)
	if err != nil {
		return nil, err
	}
	if !rule.AllAccounts() && !security.IsAccountInTenant(rule.AccountID.String(), tenantID.String()) {
		return nil, errors.New("tool approval: account does not belong to the authenticated tenant")
	}
	// A disabled rule waives nothing, so it can always be saved (for example
	// switched off after its integration was removed); its tool and target are
	// checked again when it is enabled.
	if !rule.Enabled {
		return rule, nil
	}
	if err := validateToolApprovalIdentity(ctx, rule); err != nil {
		return nil, err
	}
	return rule, nil
}

// resolveToolApprovalTool finds the tool in the rule's account, or for an
// all-accounts rule in any account of the tenant.
func resolveToolApprovalTool(rule *toolapproval.Rule, toolName string) (toolcore.NBTool, error) {
	if rule.AllAccounts() {
		if resolved, found := toolcore.GetNBTool("", toolName); found && resolved != nil && resolved.GetType() == toolcore.NBToolTypeTool {
			return resolved, nil
		}
	}
	accountIDs := []string{}
	if rule.AllAccounts() {
		ids, err := security.GetAccountIdsForTenant(rule.TenantID.String())
		if err != nil {
			return nil, fmt.Errorf("tool approval: list tenant accounts: %w", err)
		}
		accountIDs = ids
	} else {
		accountIDs = append(accountIDs, rule.AccountID.String())
	}
	for _, accountID := range accountIDs {
		resolved, found := toolcore.GetNBTool(accountID, toolName)
		if found && resolved != nil && resolved.GetType() == toolcore.NBToolTypeTool {
			return resolved, nil
		}
	}
	return nil, fmt.Errorf("tool approval: tool %q is unavailable for the selected account", rule.ToolName)
}

func validateToolApprovalIdentity(ctx *security.RequestContext, rule *toolapproval.Rule) error {
	toolName := strings.TrimSpace(rule.ToolName)
	if strings.EqualFold(toolName, "shell") {
		toolName = toolcore.ToolExecuteShellCommand
	}
	// An MCP tool belongs to one integration and is checked from saved
	// settings, without contacting its server. A built-in tool of the same name
	// wins, and the rule is always pinned to the MCP tool's own integration, so
	// it never covers a same-named tool of another integration.
	if _, builtIn := toolcore.GetNBTool("", toolName); !builtIn {
		if mcpTool, found, err := findMCPWriteTool(ctx, rule.TenantID, toolName); err != nil {
			return err
		} else if found {
			rule.ToolName = mcpTool.Name
			if !rule.AllAccounts() {
				return errors.New("tool approval: an MCP tool's rule applies to its integration; choose All integrations or Selected integrations")
			}
			if rule.ToolConfigID != nil && *rule.ToolConfigID != mcpTool.Integration.ID {
				return errors.New("tool approval: an MCP tool's rule can only be pinned to its own integration")
			}
			integrationID := mcpTool.Integration.ID
			rule.ToolConfigID = &integrationID
			return nil
		}
	}
	resolvedTool, err := resolveToolApprovalTool(rule, toolName)
	if err != nil {
		return err
	}
	rule.ToolName = resolvedTool.Name()
	if rule.AllAccounts() {
		// Unpinned, an organisation rule covers whichever target the caller
		// may use. Pinned, the target must be one of the tenant's integrations
		// of this tool: clusters and cloud accounts are chosen as accounts.
		if rule.ToolConfigID == nil {
			return nil
		}
		kind, types := toolApprovalTargetKind(ctx, resolvedTool)
		if kind != toolApprovalTargetIntegration {
			return errors.New("tool approval: an organisation rule can only be pinned to an integration; choose selected accounts to pin a cluster or cloud account")
		}
		integrations, err := toolapproval.ListTenantIntegrations(ctx.GetContext(), rule.TenantID, types)
		if err != nil {
			return fmt.Errorf("tool approval: list integrations: %w", err)
		}
		for _, integration := range integrations {
			if integration.ID == *rule.ToolConfigID {
				return nil
			}
		}
		return errors.New("tool approval: integration is unavailable for this tool")
	}

	configOwner := resolvedTool
	if strings.EqualFold(resolvedTool.Name(), toolcore.ToolExecuteShellCommand) {
		if rule.MatchKind == toolapproval.MatchTool {
			if rule.ToolConfigID != nil {
				return errors.New("tool approval: tool-wide shell rules cannot bind an execution configuration; use an exact command rule")
			}
			return nil
		}
		if rule.CommandPattern != nil {
			ownerName := tools.ShellConfigToolName(*rule.CommandPattern)
			if ownerName == "" {
				if rule.ToolConfigID != nil {
					return errors.New("tool approval: this shell command has no configurable execution target")
				}
				return nil
			}
			owner, ok := toolcore.GetNBTool(rule.AccountID.String(), ownerName)
			if !ok || owner == nil {
				return errors.New("tool approval: shell command execution target is unavailable")
			}
			configOwner = owner
		}
	}

	if _, configurable := configOwner.(toolcore.NBToolConfig); !configurable {
		if rule.ToolConfigID != nil {
			return errors.New("tool approval: this tool does not use an execution configuration")
		}
		return nil
	}
	if rule.ToolConfigID == nil {
		return errors.New("tool approval: execution configuration is required for this tool")
	}
	// A rule's account is where the call runs, so its target is chosen from
	// that account's own configurations, even for tools that can reach other
	// accounts per call: such a call matches the target account's rules.
	configs, err := toolcore.ListToolConfigs(ctx, rule.AccountID.String(), configOwner)
	if err != nil {
		return fmt.Errorf("tool approval: list execution configurations: %w", err)
	}
	for _, candidate := range configs {
		if toolcore.ToolConfigID(candidate) == *rule.ToolConfigID {
			return nil
		}
	}
	return errors.New("tool approval: execution configuration is unavailable or unauthorized")
}

func toolApprovalConfigOptions(ctx *security.RequestContext, accountID, toolName, matchKind, commandPattern string) (bool, bool, []toolApprovalConfigOption, error) {
	toolName = strings.TrimSpace(toolName)
	if strings.EqualFold(toolName, "shell") {
		toolName = toolcore.ToolExecuteShellCommand
	}
	resolvedTool, found := toolcore.GetNBTool(accountID, toolName)
	if !found || resolvedTool == nil || resolvedTool.GetType() != toolcore.NBToolTypeTool {
		return false, false, nil, fmt.Errorf("tool %q is unavailable for this account", toolName)
	}

	configOwner := resolvedTool
	if strings.EqualFold(resolvedTool.Name(), toolcore.ToolExecuteShellCommand) {
		if matchKind != string(toolapproval.MatchExactCommand) && matchKind != string(toolapproval.MatchCommandRegex) {
			return false, false, []toolApprovalConfigOption{}, nil
		}
		commandPattern = strings.TrimSpace(commandPattern)
		if commandPattern == "" {
			return false, true, []toolApprovalConfigOption{}, nil
		}
		ownerName := tools.ShellConfigToolName(commandPattern)
		if ownerName == "" {
			return false, false, []toolApprovalConfigOption{}, nil
		}
		owner, ok := toolcore.GetNBTool(accountID, ownerName)
		if !ok || owner == nil {
			return false, false, nil, errors.New("command execution target is unavailable")
		}
		configOwner = owner
	}

	if _, configurable := configOwner.(toolcore.NBToolConfig); !configurable {
		return false, false, []toolApprovalConfigOption{}, nil
	}
	configs, err := toolcore.ListToolConfigs(ctx, accountID, configOwner)
	if err != nil {
		return true, false, nil, fmt.Errorf("list execution configurations: %w", err)
	}
	options := make([]toolApprovalConfigOption, 0, len(configs))
	for _, candidate := range configs {
		id := toolcore.ToolConfigID(candidate)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(candidate.Name)
		if name == "" {
			name = id
		}
		options = append(options, toolApprovalConfigOption{ID: id, Name: name})
	}
	return true, false, options, nil
}

// mcpWriteTool is an MCP server's tool that its integration marks as a write,
// under the name the planner sees it by.
type mcpWriteTool struct {
	Name        string
	Integration toolapproval.Integration
}

// toolApprovalMCPWriteTools reads, from saved integration settings and without
// contacting the servers, the MCP tools marked as writes, and the MCP
// integrations that mark none (all their tools run without asking).
func toolApprovalMCPWriteTools(ctx *security.RequestContext, tenantID uuid.UUID) ([]mcpWriteTool, []toolapproval.Integration, error) {
	integrations, err := toolapproval.ListTenantMCPIntegrations(ctx.GetContext(), tenantID)
	if err != nil {
		return nil, nil, err
	}
	tools := []mcpWriteTool{}
	unmarked := []toolapproval.Integration{}
	for _, integration := range integrations {
		identity := toolapproval.Integration{ID: integration.ID, Name: integration.Name}
		names := toolcore.ParseToolNameList(integration.WriteTools)
		if len(names) == 0 {
			unmarked = append(unmarked, identity)
		}
		for _, name := range names {
			tools = append(tools, mcpWriteTool{Name: toolcore.MCPToolName(integration.Name, name), Integration: identity})
		}
	}
	return tools, unmarked, nil
}

// findMCPWriteTool finds a write-marked MCP tool by the name a rule uses.
func findMCPWriteTool(ctx *security.RequestContext, tenantID uuid.UUID, name string) (mcpWriteTool, bool, error) {
	tools, _, err := toolApprovalMCPWriteTools(ctx, tenantID)
	if err != nil {
		return mcpWriteTool{}, false, fmt.Errorf("tool approval: list MCP integrations: %w", err)
	}
	for _, tool := range tools {
		if strings.EqualFold(tool.Name, strings.TrimSpace(name)) {
			return tool, true, nil
		}
	}
	return mcpWriteTool{}, false, nil
}

// toolApprovalTenantTools lists the tools a rule can name: the tenant-wide
// system and custom tools. It is read once per form, independent of the
// accounts chosen, and does no per-account MCP discovery; whether a tool is
// available in each selected account is checked when the rule is saved.
func toolApprovalTenantTools(ctx *security.RequestContext) []toolApprovalToolOption {
	candidates := slices.DeleteFunc(toolcore.ListToolsForTenant(ctx), func(tool toolcore.ToolDto) bool {
		// A custom tool asks for approval only when marked as a write, so a
		// rule on a read-only one would waive nothing.
		return tool.Type == toolcore.ToolTypeCustom && !toolcore.CustomToolWrites(tool.Config)
	})
	kinds := make(map[string]string, len(candidates))
	fields := make(map[string][]toolApprovalToolField, len(candidates))
	for i := range candidates {
		if resolved, found := toolcore.GetNBTool("", candidates[i].Name); found && resolved != nil {
			_, configurable := resolved.(toolcore.NBToolConfig)
			candidates[i].NeedsConfig = configurable
			kinds[strings.TrimSpace(candidates[i].Name)], _ = toolApprovalTargetKind(ctx, resolved)
			fields[strings.TrimSpace(candidates[i].Name)] = toolApprovalFields(resolved)
		}
	}
	options := commonToolApprovalTools([][]toolcore.ToolDto{candidates})
	for i := range options {
		options[i].TargetKind = kinds[options[i].Name]
		options[i].Fields = fields[options[i].Name]
		if options[i].Fields == nil {
			options[i].Fields = []toolApprovalToolField{}
		}
		if options[i].TargetKind == "" {
			options[i].TargetKind = toolApprovalTargetNone
		}
	}
	return options
}

// toolApprovalMCPToolOptions are the picker entries for write-marked MCP tools.
func toolApprovalMCPToolOptions(tools []mcpWriteTool) []toolApprovalToolOption {
	options := make([]toolApprovalToolOption, 0, len(tools))
	for _, tool := range tools {
		options = append(options, toolApprovalToolOption{Name: tool.Name, ConfigurationMode: "none", TargetKind: toolApprovalTargetIntegration, Fields: []toolApprovalToolField{}, Source: "mcp"})
	}
	return options
}

func commonToolApprovalTools(accountTools [][]toolcore.ToolDto) []toolApprovalToolOption {
	if len(accountTools) == 0 {
		return []toolApprovalToolOption{}
	}
	counts := make(map[string]int)
	options := make(map[string]toolApprovalToolOption)
	for _, candidates := range accountTools {
		seenTools := make(map[string]struct{})
		for _, candidate := range candidates {
			if candidate.Status != toolcore.ToolStatusEnabled || candidate.NBToolType != toolcore.NBToolTypeTool {
				continue
			}
			name := strings.TrimSpace(candidate.Name)
			if name == "" {
				continue
			}
			if _, seen := seenTools[name]; seen {
				continue
			}
			seenTools[name] = struct{}{}
			mode := "none"
			if name == toolcore.ToolExecuteShellCommand {
				mode = "command"
			} else if candidate.NeedsConfig {
				mode = "required"
			}
			counts[name]++
			options[name] = toolApprovalToolOption{Name: name, ConfigurationMode: mode}
		}
	}
	result := make([]toolApprovalToolOption, 0, len(options))
	for name, option := range options {
		if counts[name] == len(accountTools) {
			result = append(result, option)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func toolApprovalListTools(c *gin.Context, ctx *security.RequestContext) {
	if !requireToolApprovalTenantReader(c, ctx) {
		return
	}
	tenantID, err := toolApprovalTenantID(ctx)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}
	mcpTools, unmarked, err := toolApprovalMCPWriteTools(ctx, tenantID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	options := append(toolApprovalTenantTools(ctx), toolApprovalMCPToolOptions(mcpTools)...)
	sort.Slice(options, func(i, j int) bool { return options[i].Name < options[j].Name })
	c.JSON(http.StatusOK, buildApiResponse(gin.H{"tools": options, "unmarked_mcp_integrations": unmarked}, nil))
}

func toolApprovalOptions(c *gin.Context, ctx *security.RequestContext, payload map[string]any) {
	if !requireToolApprovalTenantReader(c, ctx) {
		return
	}
	tenantID, err := toolApprovalTenantID(ctx)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}
	var request toolApprovalOptionsRequest
	if err := decodeToolApprovalPayload(payload, &request); err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}
	if strings.TrimSpace(request.ToolName) == "" {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{errors.New("tool approval: tool_name is required")}))
		return
	}
	if request.Organisation {
		// An MCP tool can only be pinned to its own integration; a built-in tool
		// of the same name wins.
		if _, builtIn := toolcore.GetNBTool("", strings.TrimSpace(request.ToolName)); !builtIn {
			mcpTool, found, err := findMCPWriteTool(ctx, tenantID, request.ToolName)
			if err != nil {
				c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
				return
			}
			if found {
				c.JSON(http.StatusOK, buildApiResponse(gin.H{"target_kind": toolApprovalTargetIntegration, "integrations": []toolapproval.Integration{mcpTool.Integration}}, nil))
				return
			}
		}
		// The integrations an organisation rule for this tool can be pinned to.
		probe := &toolapproval.Rule{TenantID: tenantID}
		tool, err := resolveToolApprovalTool(probe, strings.TrimSpace(request.ToolName))
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
			return
		}
		kind, types := toolApprovalTargetKind(ctx, tool)
		integrations := []toolapproval.Integration{}
		if kind == toolApprovalTargetIntegration {
			integrations, err = toolapproval.ListTenantIntegrations(ctx.GetContext(), tenantID, types)
			if err != nil {
				c.JSON(http.StatusInternalServerError, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
				return
			}
		}
		c.JSON(http.StatusOK, buildApiResponse(gin.H{"target_kind": kind, "integrations": integrations}, nil))
		return
	}
	rows := make([]toolApprovalAccountOptions, 0, len(request.AccountIDs))
	seen := make(map[string]struct{}, len(request.AccountIDs))
	for _, rawAccountID := range request.AccountIDs {
		accountID := strings.TrimSpace(rawAccountID)
		if accountID == "" {
			continue
		}
		if _, exists := seen[accountID]; exists {
			continue
		}
		seen[accountID] = struct{}{}
		row := toolApprovalAccountOptions{AccountID: accountID, Configurations: []toolApprovalConfigOption{}}
		if !security.IsAccountInTenant(accountID, tenantID.String()) {
			row.Error = "account does not belong to the authenticated tenant"
			rows = append(rows, row)
			continue
		}
		row.ConfigurationRequired, row.ConfigurationPending, row.Configurations, err = toolApprovalConfigOptions(ctx, accountID, request.ToolName, request.MatchKind, request.CommandPattern)
		if err != nil {
			row.Error = err.Error()
		}
		rows = append(rows, row)
	}
	c.JSON(http.StatusOK, buildApiResponse(gin.H{"accounts": rows}, nil))
}

func toolApprovalList(c *gin.Context, ctx *security.RequestContext) {
	if !requireToolApprovalTenantReader(c, ctx) {
		return
	}
	tenantID, err := toolApprovalTenantID(ctx)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}
	rules, err := toolapproval.List(c.Request.Context(), tenantID)
	if err != nil {
		c.JSON(500, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	// Integration names let a rule pinned to an integration show its name.
	integrationNames, err := toolapproval.TenantIntegrationNames(c.Request.Context(), tenantID)
	if err != nil {
		ctx.GetLogger().Warn("tool approval: failed to load integration names", "error", err)
		integrationNames = map[string]string{}
	}
	c.JSON(200, buildApiResponse(gin.H{"enabled": config.Config.LlmServerToolApprovalPolicyEnabled, "rules": rules, "integration_names": integrationNames}, nil))
}

func toolApprovalUpsert(c *gin.Context, ctx *security.RequestContext, payload map[string]any) {
	if !requireToolApprovalTenantAdmin(c, ctx) {
		return
	}
	tenantID, err := toolApprovalTenantID(ctx)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}
	var request toolApprovalUpsertRequest
	if err := decodeToolApprovalPayload(payload, &request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}
	rule, err := buildToolApprovalRule(ctx, request, tenantID)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}
	actor, _ := uuid.Parse(ctx.GetSecurityContext().GetUserId())
	isCreate := rule.ID == uuid.Nil
	saved, err := toolapproval.Upsert(c.Request.Context(), rule, actor)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, toolapproval.ErrRevisionConflict) {
			status = http.StatusConflict
		} else if strings.HasPrefix(err.Error(), "tool approval:") {
			status = http.StatusBadRequest
		}
		c.JSON(status, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	eventType := audit.EventTypeToolApprovalRuleUpdate
	eventAction := audit.EventActionUpdate
	if isCreate {
		eventType = audit.EventTypeToolApprovalRuleCreate
		eventAction = audit.EventActionCreate
	}
	emitToolApprovalRuleAudit(ctx, eventType, eventAction, saved)
	c.JSON(200, buildApiResponse(saved, nil))
}

// toolApprovalCreate adds the same rule to several accounts in one transaction.
func toolApprovalCreate(c *gin.Context, ctx *security.RequestContext, payload map[string]any) {
	if !requireToolApprovalTenantAdmin(c, ctx) {
		return
	}
	tenantID, err := toolApprovalTenantID(ctx)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}
	var request toolApprovalCreateRequest
	if err := decodeToolApprovalPayload(payload, &request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}
	if strings.TrimSpace(request.Rule.ID) != "" {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("tool approval: create requires a new rule")}))
		return
	}
	// One all-accounts rule, or one rule per selected account; never both.
	ruleRequests := make([]toolApprovalUpsertRequest, 0, len(request.Accounts)+1)
	if request.Rule.AllAccounts {
		if len(request.Accounts) > 0 {
			c.JSON(400, buildApiResponse(nil, []error{errors.New("tool approval: choose either all accounts or specific accounts")}))
			return
		}
		if len(request.Integrations) == 0 {
			ruleRequest := request.Rule
			ruleRequest.AccountID, ruleRequest.ToolConfigID = "", nil
			ruleRequests = append(ruleRequests, ruleRequest)
		}
		seenIntegrations := make(map[string]struct{}, len(request.Integrations))
		for _, raw := range request.Integrations {
			integrationID := strings.TrimSpace(raw)
			if _, dup := seenIntegrations[integrationID]; dup || integrationID == "" {
				c.JSON(400, buildApiResponse(nil, []error{errors.New("tool approval: invalid or duplicate integration")}))
				return
			}
			seenIntegrations[integrationID] = struct{}{}
			ruleRequest := request.Rule
			ruleRequest.AccountID, ruleRequest.ToolConfigID = "", &integrationID
			ruleRequests = append(ruleRequests, ruleRequest)
		}
	} else {
		if len(request.Integrations) > 0 {
			c.JSON(400, buildApiResponse(nil, []error{errors.New("tool approval: integrations are chosen for organisation rules only")}))
			return
		}
		if len(request.Accounts) == 0 {
			c.JSON(400, buildApiResponse(nil, []error{errors.New("tool approval: choose all accounts or at least one account")}))
			return
		}
		seen := make(map[string]struct{}, len(request.Accounts))
		for _, account := range request.Accounts {
			key := strings.TrimSpace(account.AccountID)
			if _, dup := seen[key]; dup {
				c.JSON(400, buildApiResponse(nil, []error{errors.New("tool approval: duplicate account")}))
				return
			}
			seen[key] = struct{}{}
			ruleRequest := request.Rule
			ruleRequest.AccountID, ruleRequest.ToolConfigID = account.AccountID, account.ToolConfigID
			ruleRequests = append(ruleRequests, ruleRequest)
		}
	}
	rules := make([]*toolapproval.Rule, 0, len(ruleRequests))
	for _, ruleRequest := range ruleRequests {
		rule, err := buildToolApprovalRule(ctx, ruleRequest, tenantID)
		if err != nil {
			c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
			return
		}
		rules = append(rules, rule)
	}
	actor, _ := uuid.Parse(ctx.GetSecurityContext().GetUserId())
	saved, err := toolapproval.Create(c.Request.Context(), rules, actor)
	if err != nil {
		status := http.StatusInternalServerError
		if strings.HasPrefix(err.Error(), "tool approval:") {
			status = http.StatusBadRequest
		}
		c.JSON(status, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	for _, rule := range saved {
		emitToolApprovalRuleAudit(ctx, audit.EventTypeToolApprovalRuleCreate, audit.EventActionCreate, rule)
	}
	c.JSON(200, buildApiResponse(gin.H{"rules": saved}, nil))
}

func toolApprovalDelete(c *gin.Context, ctx *security.RequestContext, payload map[string]any) {
	if !requireToolApprovalTenantAdmin(c, ctx) {
		return
	}
	tenantID, err := toolApprovalTenantID(ctx)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}
	var request toolApprovalDeleteRequest
	if err := decodeToolApprovalPayload(payload, &request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}
	id, err := uuid.Parse(request.ID)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{errors.New("tool approval: invalid rule id")}))
		return
	}
	actor, _ := uuid.Parse(ctx.GetSecurityContext().GetUserId())
	deleted, err := toolapproval.Delete(c.Request.Context(), tenantID, id, request.Revision, actor)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, toolapproval.ErrRevisionConflict) {
			status = http.StatusConflict
		}
		c.JSON(status, buildApiResponse(nil, []error{common.Error{Message: err.Error()}}))
		return
	}
	if deleted != nil {
		emitToolApprovalRuleAudit(ctx, audit.EventTypeToolApprovalRuleDelete, audit.EventActionDelete, deleted)
	}
	c.JSON(200, buildApiResponse(gin.H{"deleted": deleted != nil}, nil))
}

// toolApprovalRuleAccount is the audit account for a rule: empty for an
// all-accounts rule, whose scope the audit state records instead.
func toolApprovalRuleAccount(rule *toolapproval.Rule) string {
	if rule.AllAccounts() {
		return ""
	}
	return rule.AccountID.String()
}

func emitToolApprovalRuleAudit(ctx *security.RequestContext, eventType audit.EventType, action audit.EventAction, rule *toolapproval.Rule) {
	if ctx == nil || ctx.GetSecurityContext() == nil || rule == nil {
		return
	}
	request := &audit.AuditRequest{Audits: []audit.Audit{{
		UserId: ctx.GetSecurityContext().GetUserId(), TenantId: rule.TenantID.String(), AccountId: toolApprovalRuleAccount(rule),
		EventTime: time.Now().UTC(), EventCategory: audit.EventCategorySecurity, EventType: eventType,
		EventAction: action, EventActor: audit.EventActorUiService, EventTarget: rule.ID.String(), EventStatus: audit.EventStatusSuccess,
		EventState:    map[string]any{"rule_id": rule.ID.String(), "rule_revision": rule.Revision, "rule_name": rule.Name, "tool_name": rule.ToolName, "tool_config_id": rule.ToolConfigID, "match_kind": rule.MatchKind, "enabled": rule.Enabled, "all_accounts": rule.AllAccounts(), "allow_unattended": rule.AllowUnattended},
		TransactionId: ctx.GetTraceId(),
	}}}
	if err := audit.CreateAudit(ctx, request); err != nil {
		ctx.GetLogger().Error("tool approval: failed to audit rule mutation", "rule_id", rule.ID, "event_type", eventType, "error", err)
	}
}

func toolApprovalCheck(c *gin.Context, ctx *security.RequestContext, payload map[string]any) {
	if !requireToolApprovalTenantReader(c, ctx) {
		return
	}
	tenantID, err := toolApprovalTenantID(ctx)
	if err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}
	var request toolApprovalCheckRequest
	if err := decodeToolApprovalPayload(payload, &request); err != nil {
		c.JSON(400, buildApiResponse(nil, []error{err}))
		return
	}
	rule, err := buildToolApprovalRule(ctx, request.Rule, tenantID)
	if err != nil {
		c.JSON(http.StatusBadRequest, buildApiResponse(nil, []error{err}))
		return
	}
	targetID := ""
	if rule.ToolConfigID != nil {
		targetID = *rule.ToolConfigID
	}
	matched, reason := toolapproval.MatchRule(*rule, rule.ToolName, request.Input, targetID)
	c.JSON(200, buildApiResponse(gin.H{"matched": matched, "reason": reason}, nil))
}

func handleToolApprovalConfigApis(r *gin.Engine, tracer trace.Tracer, meter metric.Meter) {
	group := r.Group("/v1/tool-approval/config")
	group.POST("", func(c *gin.Context) {
		var actionRequest ActionRequest
		if err := c.ShouldBindJSON(&actionRequest); err != nil {
			c.JSON(400, buildApiResponse(nil, []error{common.Error{Message: "tool approval: " + err.Error()}}))
			return
		}
		ctx, err := buildContextFromPayload(c.Request.Context(), c, &actionRequest, tracer, meter, slog.With())
		if err != nil {
			c.JSON(401, buildApiResponse(nil, []error{err}))
			return
		}
		payload := actionRequest.Input
		if raw, ok := payload["request"].(map[string]any); ok {
			payload = raw
		}
		switch actionRequest.Action.Name {
		case "toolapproval_list":
			toolApprovalList(c, ctx)
		case "toolapproval_create":
			toolApprovalCreate(c, ctx, payload)
		case "toolapproval_upsert":
			toolApprovalUpsert(c, ctx, payload)
		case "toolapproval_delete":
			toolApprovalDelete(c, ctx, payload)
		case "toolapproval_check_match":
			toolApprovalCheck(c, ctx, payload)
		case "toolapproval_list_options":
			toolApprovalOptions(c, ctx, payload)
		case "toolapproval_list_tools":
			toolApprovalListTools(c, ctx)
		default:
			c.JSON(400, buildApiResponse(nil, []error{errors.New("tool approval: unsupported action")}))
		}
	})
}
