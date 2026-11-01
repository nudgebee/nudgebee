package tools

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

const (
	ToolVMList    = "vm_list_execute"
	ToolVMExecute = "vm_execute"
)

func init() {
	core.RegisterNBToolFactory(ToolVMList, func(string) (core.NBTool, error) { return VMListTool{}, nil })
	core.RegisterNBToolFactory(ToolVMExecute, func(string) (core.NBTool, error) { return VMExecuteTool{}, nil })
}

type VMListTool struct{}

func (VMListTool) Name() string             { return ToolVMList }
func (VMListTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (VMListTool) Description() string {
	return "List active VM inventory records in the selected VM account, including inventory status and last-seen time. These fields do not establish live SSH connectivity. Use this to resolve exact resource IDs before remote commands. Results are bounded to 25."
}
func (VMListTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
		"search": {Type: core.ToolSchemaTypeString, Description: "Optional partial VM name or resource ID."},
	}}
}

type vmInventoryRow struct {
	ID              string     `db:"id" json:"resource_id"`
	Name            string     `db:"name" json:"name"`
	ResourceID      string     `db:"resourse_id" json:"inventory_id"`
	InventoryStatus string     `db:"status" json:"inventory_status"`
	LastSeen        *time.Time `db:"last_seen" json:"last_seen"`
}

func vmAccount(ctx core.NbToolContext) (string, error) {
	if ctx.Ctx == nil || ctx.Ctx.GetSecurityContext() == nil || ctx.AccountId == "" {
		return "", errors.New("vm: account context is required")
	}
	tenantID := ctx.Ctx.GetSecurityContext().GetTenantId()
	if tenantID == "" || !ctx.Ctx.GetSecurityContext().HasAccountAccess(ctx.AccountId, "read") || !security.IsAccountInTenant(ctx.AccountId, tenantID) {
		return "", errors.New("vm: account is not accessible")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("vm: database: %w", err)
	}
	var accountType string
	if err := dbms.Db.GetContext(ctx.GoContext(), &accountType, `SELECT account_type FROM cloud_accounts WHERE id = $1 AND tenant = $2 AND status = 'active'`, ctx.AccountId, tenantID); err != nil {
		return "", fmt.Errorf("vm: active account lookup: %w", err)
	}
	if !strings.EqualFold(accountType, "vm") {
		return "", errors.New("vm: selected account is not a VM account")
	}
	return tenantID, nil
}

// VMAccountTargetByName resolves a connected VM account by name for a
// per-call or handed target, among the active VM accounts of the caller's
// tenant that the caller can read.
func VMAccountTargetByName(ctx *security.RequestContext, accountID, requested string) (core.ToolConfig, error) {
	if ctx == nil || ctx.GetSecurityContext() == nil {
		return core.ToolConfig{}, errors.New("vm account lookup: caller security context is required")
	}
	sc := ctx.GetSecurityContext()
	tenantID := sc.GetTenantId()
	if tenantID == "" || !security.IsAccountInTenant(accountID, tenantID) {
		return core.ToolConfig{}, errors.New("vm account lookup: caller tenant is required")
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return core.ToolConfig{}, fmt.Errorf("vm account lookup: database: %w", err)
	}
	accounts := []struct {
		ID   string `db:"id"`
		Name string `db:"account_name"`
	}{}
	if err := dbms.Db.SelectContext(ctx.GetContext(), &accounts, `SELECT id::text AS id, account_name FROM cloud_accounts
		WHERE tenant = $1 AND status = 'active' AND account_type = 'vm' ORDER BY account_name`, tenantID); err != nil {
		return core.ToolConfig{}, fmt.Errorf("vm account lookup: %w", err)
	}
	names := make([]string, 0, len(accounts))
	for _, a := range accounts {
		if !sc.HasAccountAccess(a.ID, security.SecurityAccessTypeRead) {
			continue
		}
		if strings.EqualFold(a.Name, requested) {
			return core.ToolConfig{Name: a.Name, Values: []core.ToolConfigValue{{Name: "id", Value: a.ID}}}, nil
		}
		names = append(names, a.Name)
	}
	if len(names) == 0 {
		return core.ToolConfig{}, fmt.Errorf("VM account %q is not connected or you do not have access to it; no VM accounts are available to you", requested)
	}
	return core.ToolConfig{}, fmt.Errorf("VM account %q is not connected or you do not have access to it; available VM accounts: %s", requested, strings.Join(names, ", "))
}

func vmToolError(err error) (core.NBToolResponse, error) {
	return core.NBToolResponse{Data: err.Error(), Status: core.NBToolResponseStatusError}, err
}

func (VMListTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	ctx = ctx.ForTarget()
	tenantID, err := vmAccount(ctx)
	if err != nil {
		return vmToolError(err)
	}
	search, _ := input.Arguments["search"].(string)
	search = strings.TrimSpace(search)
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return vmToolError(fmt.Errorf("vm list: database: %w", err))
	}
	rows := []vmInventoryRow{}
	err = dbms.Db.SelectContext(ctx.GoContext(), &rows, `SELECT id::text AS id, name, resourse_id, COALESCE(status, '') AS status, last_seen FROM cloud_resourses
		WHERE tenant = $1 AND account = $2 AND is_active IS TRUE AND type IN ('VM', 'compute-instance')
		  AND ($3 = '' OR name ILIKE '%' || $3 || '%' OR resourse_id ILIKE '%' || $3 || '%')
		ORDER BY name, id LIMIT 26`, tenantID, ctx.AccountId, search)
	if err != nil {
		return vmToolError(fmt.Errorf("vm list: inventory: %w", err))
	}
	truncated := len(rows) > 25
	if truncated {
		rows = rows[:25]
	}
	data, err := json.Marshal(map[string]any{"vms": rows, "truncated": truncated})
	if err != nil {
		return vmToolError(fmt.Errorf("vm list: encode: %w", err))
	}
	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: core.NBToolResponseStatusSuccess}, nil
}

type VMExecuteTool struct{}

func (VMExecuteTool) Name() string             { return ToolVMExecute }
func (VMExecuteTool) GetType() core.NBToolType { return core.NBToolTypeTool }
func (VMExecuteTool) Description() string {
	return "Run one non-interactive command on one inventoried VM, using an account-linked dynamic SSH proxy. Requires the VM resource ID returned by vm_list_execute. Reports the exact target and datasource used."
}
func (VMExecuteTool) InputSchema() core.ToolSchema {
	return core.ToolSchema{Type: core.ToolSchemaTypeObject, Properties: map[string]core.ToolSchemaProperty{
		"resource_id":   {Type: core.ToolSchemaTypeString, Description: "Exact VM resource UUID from vm_list_execute."},
		"command":       {Type: core.ToolSchemaTypeString, Description: "Non-interactive shell command to run on the VM."},
		"datasource_id": {Type: core.ToolSchemaTypeString, Description: "Optional SSH datasource UUID, required if several eligible datasources can reach this VM."},
	}, Required: []string{"resource_id", "command"}}
}

func vmExecuteInput(input core.NBToolCallRequest) (resourceID, command, datasourceID string) {
	resourceID, _ = input.Arguments["resource_id"].(string)
	resourceID = strings.TrimSpace(resourceID)
	command = input.Command
	if command == "" {
		command, _ = input.Arguments["command"].(string)
	}
	datasourceID, _ = input.Arguments["datasource_id"].(string)
	datasourceID = strings.TrimSpace(datasourceID)
	return
}

func (VMExecuteTool) InferToolRequestTypePrompt(ctx *security.RequestContext, toolName, input string) (string, error) {
	prompt, err := ServerExecuteTool{}.InferToolRequestTypePrompt(ctx, toolName, input)
	if err != nil {
		return "", err
	}
	return "The input is a JSON object for vm_execute. Classify the shell command in its command field using the following rules. Ignore resource_id and datasource_id when determining intent. If the command field is absent or cannot be classified, answer unknown.\n\n" + prompt, nil
}

// Bind a write approval to the exact inventory target, datasource choice, and
// command. A later command or VM in the same conversation needs fresh consent.
func (VMExecuteTool) ConfirmationKey(toolInput string) string {
	return perActionConfirmationKey(ToolVMExecute, toolInput)
}

func (VMExecuteTool) ConfirmationQuestion(toolInput string) string {
	args := confirmationArgs(toolInput)
	resourceID, _ := args["resource_id"].(string)
	command, _ := args["command"].(string)
	if strings.TrimSpace(resourceID) == "" || strings.TrimSpace(command) == "" {
		return ""
	}
	datasourceID, _ := args["datasource_id"].(string)
	if datasourceID != "" {
		return fmt.Sprintf("Run command %q on VM resource %s using SSH datasource %s?", command, resourceID, datasourceID)
	}
	return fmt.Sprintf("Run command %q on VM resource %s using its eligible SSH datasource?", command, resourceID)
}

type vmTarget struct {
	Name       string `db:"name"`
	ResourceID string `db:"resourse_id"`
	Meta       []byte `db:"meta"`
}

func vmAddresses(target vmTarget) ([]netip.Addr, error) {
	if ip, ok := strings.CutPrefix(target.ResourceID, "vm-"); ok {
		if addr, err := netip.ParseAddr(ip); err == nil {
			return []netip.Addr{addr.Unmap()}, nil
		}
	}
	var meta map[string]any
	if err := json.Unmarshal(target.Meta, &meta); err != nil {
		return nil, errors.New("VM has no valid address metadata")
	}
	var addresses []netip.Addr
	for _, key := range []string{"PrivateIpAddress", "PublicIpAddress"} {
		if raw, ok := meta[key].(string); ok {
			if addr, err := netip.ParseAddr(raw); err == nil {
				addr = addr.Unmap()
				if len(addresses) == 0 || addresses[0] != addr {
					addresses = append(addresses, addr)
				}
			}
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("VM has no verified IP address")
	}
	return addresses, nil
}

type vmSSHSource struct {
	ID     string `db:"id"`
	Name   string `db:"name"`
	Labels []byte `db:"labels"`
	Key    string `db:"datasource_key"`
	Agent  string `db:"agent_type"`
}

var errNoEligibleVMSSHSource = errors.New("no eligible account-linked dynamic SSH datasource covers this VM")

func vmSSHAllows(labels []byte, ip netip.Addr) bool {
	var metadata struct {
		Mode         string   `json:"mode"`
		AllowedHosts []string `json:"allowed_hosts"`
	}
	if json.Unmarshal(labels, &metadata) != nil || metadata.Mode != "dynamic" || len(metadata.AllowedHosts) == 0 {
		return false
	}
	for _, raw := range metadata.AllowedHosts {
		if addr, err := netip.ParseAddr(raw); err == nil && addr.Unmap() == ip {
			return true
		}
		if prefix, err := netip.ParsePrefix(raw); err == nil && prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func chooseVMSSHSource(sources []vmSSHSource, requestedID string, addresses []netip.Addr) (vmSSHSource, netip.Addr, error) {
	if requestedID != "" {
		for _, source := range sources {
			if source.ID != requestedID {
				continue
			}
			if source.Key == "" {
				return vmSSHSource{}, netip.Addr{}, fmt.Errorf("requested SSH datasource %s has no datasource key", requestedID)
			}
			for _, ip := range addresses {
				if vmSSHAllows(source.Labels, ip) {
					return source, ip, nil
				}
			}
			return vmSSHSource{}, netip.Addr{}, fmt.Errorf("requested SSH datasource %s does not permit an inventoried VM address", requestedID)
		}
		return vmSSHSource{}, netip.Addr{}, fmt.Errorf("requested SSH datasource %s is not enabled and linked to this account", requestedID)
	}

	// Prefer the first inventoried address with a unique eligible datasource.
	// Ambiguity on that address requires an explicit choice; trying the next
	// address could silently use different credentials for the same VM.
	for _, ip := range addresses {
		var eligible []vmSSHSource
		for _, source := range sources {
			if source.Key != "" && vmSSHAllows(source.Labels, ip) {
				eligible = append(eligible, source)
			}
		}
		if len(eligible) == 1 {
			return eligible[0], ip, nil
		}
		if len(eligible) > 1 {
			identities := make([]string, 0, len(eligible))
			for _, source := range eligible {
				identities = append(identities, source.Name+" ("+source.ID+")")
			}
			return vmSSHSource{}, netip.Addr{}, fmt.Errorf("multiple SSH datasources cover this VM: %s; supply datasource_id", strings.Join(identities, ", "))
		}
	}
	return vmSSHSource{}, netip.Addr{}, errNoEligibleVMSSHSource
}

func (VMExecuteTool) Call(ctx core.NbToolContext, input core.NBToolCallRequest) (core.NBToolResponse, error) {
	ctx = ctx.ForTarget()
	tenantID, err := vmAccount(ctx)
	if err != nil {
		return vmToolError(err)
	}
	resourceID, command, requestedSource := vmExecuteInput(input)
	if strings.TrimSpace(resourceID) == "" || strings.TrimSpace(command) == "" {
		return vmToolError(errors.New("vm execute: resource_id and command are required"))
	}
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return vmToolError(fmt.Errorf("vm execute: database: %w", err))
	}
	var target vmTarget
	err = dbms.Db.GetContext(ctx.GoContext(), &target, `SELECT name, resourse_id, meta FROM cloud_resourses WHERE id = $1 AND account = $2 AND tenant = $3 AND is_active IS TRUE AND type IN ('VM', 'compute-instance')`, resourceID, ctx.AccountId, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return vmToolError(errors.New("vm execute: VM is not active in the selected account"))
	}
	if err != nil {
		return vmToolError(fmt.Errorf("vm execute: inventory lookup: %w", err))
	}
	addresses, err := vmAddresses(target)
	if err != nil {
		return vmToolError(fmt.Errorf("vm execute: %w", err))
	}
	var sources []vmSSHSource
	err = dbms.Db.SelectContext(ctx.GoContext(), &sources, `SELECT i.id::text AS id, i.name, i.labels,
		COALESCE((SELECT value FROM integration_config_values WHERE integration_id = i.id AND name = 'datasource_key'), '') AS datasource_key,
		COALESCE((SELECT value FROM integration_config_values WHERE integration_id = i.id AND name = 'agent_type'), 'proxy') AS agent_type
		FROM integrations i JOIN integrations_cloud_accounts ia ON ia.integration_id = i.id
		WHERE ia.cloud_account_id = $1 AND ia.tenant_id = $2 AND ia.link_role = 'own'
		AND i.tenant_id = $2 AND i.type = 'ssh' AND i.source = 'agent' AND i.status = 'enabled'
		AND EXISTS (SELECT 1 FROM integration_config_values WHERE integration_id = i.id AND name = 'connection_mode' AND value = 'vm_agent')`, ctx.AccountId, tenantID)
	if err != nil {
		return vmToolError(fmt.Errorf("vm execute: SSH datasource lookup: %w", err))
	}
	source, ip, err := chooseVMSSHSource(sources, requestedSource, addresses)
	if err != nil {
		return vmToolError(fmt.Errorf("vm execute: %w", err))
	}
	ctx.ToolConfig = core.ToolConfig{Id: source.ID, Name: source.Name, Values: []core.ToolConfigValue{
		{Name: "connection_mode", Value: "vm_agent"}, {Name: "agent_type", Value: source.Agent}, {Name: "datasource_key", Value: source.Key},
	}}
	started := time.Now().UTC()
	result, err := ExecuteContainerJob(ctx, RelayJobSSH, command, ctx.AccountId, map[string]any{"host_name": ip.String()}, false)
	if err != nil {
		return vmToolError(fmt.Errorf("vm execute: resource %s (%s at %s) via datasource %s (%s): %w", resourceID, target.Name, ip, source.ID, source.Name, err))
	}
	rawResult, ok := result.(string)
	if !ok {
		return vmToolError(errors.New("vm execute: unexpected SSH result"))
	}
	var commandResult struct {
		Stdout   string `json:"stdout"`
		Stderr   string `json:"stderr"`
		ExitCode *int   `json:"exit_code"`
	}
	if err := json.Unmarshal([]byte(rawResult), &commandResult); err != nil {
		return vmToolError(fmt.Errorf("vm execute: decode SSH result: %w", err))
	}
	if commandResult.ExitCode == nil {
		return vmToolError(errors.New("vm execute: SSH result has no exit code"))
	}
	data, err := json.Marshal(map[string]any{"resource_id": resourceID, "vm_name": target.Name, "address": ip.String(), "datasource_id": source.ID, "datasource_name": source.Name, "collected_at": started.Format(time.RFC3339Nano), "stdout": commandResult.Stdout, "stderr": commandResult.Stderr, "exit_code": commandResult.ExitCode})
	if err != nil {
		return vmToolError(fmt.Errorf("vm execute: encode: %w", err))
	}
	status := core.NBToolResponseStatusSuccess
	if *commandResult.ExitCode != 0 {
		status = core.NBToolResponseStatusError
	}
	return core.NBToolResponse{Data: string(data), Type: core.NBToolResponseTypeJson, Status: status}, nil
}
