package tools

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// A forager discovery scope with ssh_access exposes a dynamic-mode ssh-proxy
// datasource. Relay registers it as one `ssh` integration (connection_mode=
// vm_agent, no `host`) and writes the proxy metadata {mode, port,
// allowed_hosts, active_connections} into integrations.labels, which
// ListToolConfigs loads into ToolConfig.Tags. Every request to such an
// integration must name its target host, which forager checks against
// allowed_hosts. The helpers below turn a discovered VM (a cloud_resourses
// row) into that host.

// sshDiscoverySuffix is the suffix forager appends to a discovery
// datasource id to form its ssh access sibling's id.
const sshDiscoverySuffix = ":ssh"

// discoveredVM is one active VM row a dynamic ssh integration can reach.
type discoveredVM struct {
	ResourceID string
	Name       string
	IP         string
	DNSName    string
	AccountID  string
	LastSeen   time.Time
	// Host is what the request names as the target: the IP when it is in
	// allowed_hosts, else the DNS name when that is. forager matches a
	// hostname entry by exact name, so a VM allowed only by name must be
	// addressed by name. Set by reachableVMs.
	Host string
}

// label names the VM for people: its resource id, plus the name when it adds
// something.
func (v discoveredVM) label() string {
	if v.Name != "" && !strings.EqualFold(v.Name, v.ResourceID) && !strings.EqualFold(v.Name, v.IP) {
		return v.ResourceID + " " + v.Name
	}
	return v.ResourceID
}

// matches reports whether instance names this VM by resource id, name, IP or
// DNS name, ignoring case.
func (v discoveredVM) matches(instance string) bool {
	instance = strings.TrimSuffix(strings.TrimSpace(instance), ".")
	if instance == "" {
		return false
	}
	for _, candidate := range []string{v.ResourceID, v.Name, v.IP, strings.TrimSuffix(v.DNSName, ".")} {
		if candidate != "" && strings.EqualFold(candidate, instance) {
			return true
		}
	}
	return false
}

// isDynamicSSHConfig reports whether cfg is a forager ssh-proxy integration in
// dynamic mode: every request must carry the target host.
func isDynamicSSHConfig(cfg core.ToolConfig) bool {
	return isVMAgentMode(cfg.Values) &&
		strings.TrimSpace(getConfigValue(cfg.Values, "host")) == "" &&
		strings.EqualFold(cfg.Tags["mode"], "dynamic")
}

// withHostValue returns a copy of cfg whose `host` value is host. The Values
// slice is copied so the cached config is not changed.
func withHostValue(cfg core.ToolConfig, host string) core.ToolConfig {
	values := make([]core.ToolConfigValue, 0, len(cfg.Values)+1)
	for _, v := range cfg.Values {
		if v.Name != "host" {
			values = append(values, v)
		}
	}
	cfg.Values = append(values, core.ToolConfigValue{Name: "host", Value: host})
	return cfg
}

// parseAllowedHosts reads the allowed_hosts label. Labels are flattened to
// strings with fmt's %v, so a JSON array arrives as "[10.0.0.0/24 host-a]";
// a JSON array string or a comma-separated list is accepted too.
func parseAllowedHosts(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var list []string
	if err := json.Unmarshal([]byte(raw), &list); err == nil {
		return nonEmpty(list)
	}
	raw = strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
	return nonEmpty(strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }))
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// hostAllowed reports whether ip is inside one of the allowed CIDRs or equals
// an allowed host. An empty allow list allows nothing: forager treats it as
// "anywhere", which is not a scope Nubi should widen into a shell target.
func hostAllowed(ip string, allowed []string) bool {
	parsed := net.ParseIP(ip)
	for _, entry := range allowed {
		if _, network, err := net.ParseCIDR(entry); err == nil {
			if parsed != nil && network.Contains(parsed) {
				return true
			}
			continue
		}
		if strings.EqualFold(entry, ip) {
			return true
		}
	}
	return false
}

// sshVMLister lists the active VMs in the accounts linked to a dynamic ssh
// integration. A variable so tests can replace the database lookup.
var sshVMLister = listSSHIntegrationVMs

// listSSHIntegrationVMs reads the VMs from cloud_resourses in every account
// linked to the ssh integration, plus the accounts linked to its sibling
// discovery integration (same datasource_key without ":ssh"). Discovery
// files matched VMs under its discovery_target account, which the ssh
// sibling is not necessarily linked to.
func listSSHIntegrationVMs(ctx *security.RequestContext, cfg core.ToolConfig) ([]discoveredVM, error) {
	if cfg.Id == "" {
		return nil, nil
	}
	discoveryKey := ""
	if key := getConfigValue(cfg.Values, "datasource_key"); strings.HasSuffix(key, sshDiscoverySuffix) {
		discoveryKey = strings.TrimSuffix(key, sshDiscoverySuffix)
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return nil, err
	}
	rows, err := dbms.Query(`
		WITH owner AS (
			SELECT tenant_id FROM integrations WHERE id = $1::uuid
		), linked AS (
			SELECT ica.cloud_account_id FROM integrations_cloud_accounts ica WHERE ica.integration_id = $1::uuid
			UNION
			SELECT ica.cloud_account_id
			FROM integrations d
			JOIN integration_config_values icv ON icv.integration_id = d.id AND icv.name = 'datasource_key'
			JOIN integrations_cloud_accounts ica ON ica.integration_id = d.id
			WHERE $2 <> '' AND icv.value = $2
			  AND d.type = 'discovery' AND d.status = 'enabled'
			  AND d.tenant_id = (SELECT tenant_id FROM owner)
		)
		SELECT cr.resourse_id,
		       COALESCE(cr.name, ''),
		       COALESCE(cr.meta->>'PrivateIpAddress', ''),
		       COALESCE(NULLIF(cr.meta->>'PrivateDnsName', ''), cr.meta->>'rdns', ''),
		       cr.account::text,
		       COALESCE(cr.last_seen, cr.updated_at, cr.created_at)
		FROM cloud_resourses cr
		WHERE cr.account IN (SELECT cloud_account_id FROM linked)
		  AND cr.tenant = (SELECT tenant_id FROM owner)
		  AND cr.is_active IS NOT FALSE
		  AND cr.type IN ('compute-instance', 'VM')
		  AND COALESCE(cr.meta->>'PrivateIpAddress', '') <> ''
		ORDER BY 6 DESC NULLS LAST
		LIMIT 1000`, cfg.Id, discoveryKey)
	if err != nil {
		return nil, fmt.Errorf("list VMs for ssh integration %s: %w", cfg.Name, err)
	}
	defer func() { _ = rows.Close() }()

	var vms []discoveredVM
	for rows.Next() {
		var vm discoveredVM
		var lastSeen *time.Time
		if err := rows.Scan(&vm.ResourceID, &vm.Name, &vm.IP, &vm.DNSName, &vm.AccountID, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan VM for ssh integration %s: %w", cfg.Name, err)
		}
		if lastSeen != nil {
			vm.LastSeen = *lastSeen
		}
		vms = append(vms, vm)
	}
	return vms, rows.Err()
}

// reachableVMs returns the VMs of a dynamic ssh integration whose IP (or DNS
// name) is inside its allowed_hosts and whose account the caller can read. outOfScope holds
// the VMs that were found but are outside allowed_hosts.
func reachableVMs(ctx *security.RequestContext, cfg core.ToolConfig) (reachable, outOfScope []discoveredVM, err error) {
	vms, err := sshVMLister(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	allowed := parseAllowedHosts(cfg.Tags["allowed_hosts"])
	for _, vm := range vms {
		if !canReadAccount(ctx, vm.AccountID) {
			continue
		}
		dns := strings.TrimSuffix(vm.DNSName, ".")
		switch {
		case hostAllowed(vm.IP, allowed):
			vm.Host = vm.IP
			reachable = append(reachable, vm)
		case dns != "" && hostAllowed(dns, allowed):
			vm.Host = dns
			reachable = append(reachable, vm)
		default:
			outOfScope = append(outOfScope, vm)
		}
	}
	return reachable, outOfScope, nil
}

func canReadAccount(ctx *security.RequestContext, accountID string) bool {
	// Fail closed: system flows run with a tenant-admin context, never nil.
	if ctx == nil || ctx.GetSecurityContext() == nil {
		return false
	}
	return security.ToolPathAccountAccess(ctx, accountID, "cloud", security.SecurityAccessTypeRead)
}

// vmMatch is one (integration, VM) pair an instance name resolved to.
type vmMatch struct {
	Config core.ToolConfig
	VM     discoveredVM
}

// resolveVMTarget resolves instance to a discovered VM reachable through one
// of the dynamic ssh integrations in configs, and returns that integration
// with `host` set to the VM's IP.
//
// It returns an empty config and no error when no integration is dynamic or
// no VM matches, so callers fall through to their other strategies. When the
// VM exists but is outside every allowed_hosts, or matches several targets
// that cannot be told apart, it returns an error wrapping
// core.ErrNoMatchingConfig so the planner shows the reason instead of picking
// one.
func resolveVMTarget(ctx core.NbToolContext, instance string, configs []core.ToolConfig) (core.ToolConfig, error) {
	var matches []vmMatch
	var outside []vmMatch
	for _, cfg := range configs {
		if !isDynamicSSHConfig(cfg) {
			continue
		}
		reachable, outOfScope, err := reachableVMs(ctx.Ctx, cfg)
		if err != nil {
			if ctx.Ctx != nil {
				ctx.Ctx.GetLogger().Warn("server: VM lookup failed", "integration", cfg.Name, "error", err)
			}
			continue
		}
		for _, vm := range reachable {
			if vm.matches(instance) {
				matches = append(matches, vmMatch{Config: cfg, VM: vm})
			}
		}
		for _, vm := range outOfScope {
			if vm.matches(instance) {
				outside = append(outside, vmMatch{Config: cfg, VM: vm})
			}
		}
	}
	matches = dedupeVMMatches(matches)

	if len(matches) == 0 {
		if len(outside) > 0 {
			return core.ToolConfig{}, fmt.Errorf("%w: no ssh access for %s: %s is outside the allowed hosts of %s",
				core.ErrNoMatchingConfig, instance, outside[0].VM.IP, outside[0].Config.Name)
		}
		return core.ToolConfig{}, nil
	}
	if len(matches) > 1 {
		var local []vmMatch
		for _, m := range matches {
			if m.VM.AccountID == ctx.AccountId {
				local = append(local, m)
			}
		}
		if len(local) == 1 {
			matches = local
		}
	}
	if len(matches) > 1 {
		candidates := make([]string, 0, len(matches))
		for _, m := range matches {
			candidates = append(candidates, fmt.Sprintf("%s (host %s, %s)", m.Config.Name, m.VM.Host, m.VM.label()))
		}
		sortNames(candidates)
		return core.ToolConfig{}, fmt.Errorf(`%w: %s matches several ssh targets: %s. Pass instance="<integration name>" with host_name="<ip>" to choose one`,
			core.ErrNoMatchingConfig, instance, strings.Join(candidates, "; "))
	}
	return withHostValue(matches[0].Config, matches[0].VM.Host), nil
}

// dedupeVMMatches drops repeats of the same integration and IP, which happen
// when a VM has both a discovery placeholder row and a cloud-collector row.
func dedupeVMMatches(matches []vmMatch) []vmMatch {
	seen := map[string]bool{}
	out := matches[:0]
	for _, m := range matches {
		key := m.Config.Name + "\x00" + m.VM.Host
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	return out
}

// resolveCallHost sets the target host on a dynamic ssh integration for one
// call when the caller did not pass host_name: the planner stores only the
// integration name, so the host IdentifyConfig resolved has to be derived
// again from the instance argument, within this integration only.
func resolveCallHost(ctx core.NbToolContext, instance string) (core.ToolConfig, error) {
	cfg := ctx.ToolConfig
	if !isDynamicSSHConfig(cfg) || strings.EqualFold(instance, cfg.Name) {
		return cfg, nil
	}
	resolved, err := resolveVMTarget(ctx, instance, []core.ToolConfig{cfg})
	if err != nil {
		return cfg, err
	}
	if resolved.Name == "" {
		return cfg, nil
	}
	return resolved, nil
}

// maxListedVMTargets caps how many discovered VMs the execution-targets block
// names.
const maxListedVMTargets = 50

// sshVMTargets lists the discovered VMs reachable through the dynamic ssh
// integrations of the account, most recently seen first, one entry per IP.
func sshVMTargets(ctx *security.RequestContext, accountID string) ([]vmMatch, error) {
	tool, ok := core.GetNBTool(accountID, ToolExecuteServerCommand)
	if !ok {
		return nil, nil
	}
	configs, err := core.ListToolConfigs(ctx, accountID, tool)
	if err != nil {
		return nil, err
	}
	var all []vmMatch
	for _, cfg := range configs {
		if !isDynamicSSHConfig(cfg) {
			continue
		}
		reachable, _, err := reachableVMs(ctx, cfg)
		if err != nil {
			ctx.GetLogger().Warn("execution targets: VM lookup failed", "integration", cfg.Name, "error", err)
			continue
		}
		for _, vm := range reachable {
			all = append(all, vmMatch{Config: cfg, VM: vm})
		}
	}
	return orderVMTargets(all), nil
}

// orderVMTargets sorts VMs most recently seen first and keeps one entry per IP.
func orderVMTargets(all []vmMatch) []vmMatch {
	sort.SliceStable(all, func(i, j int) bool { return all[i].VM.LastSeen.After(all[j].VM.LastSeen) })
	seen := map[string]bool{}
	out := make([]vmMatch, 0, len(all))
	for _, m := range all {
		if seen[m.VM.Host] {
			continue
		}
		seen[m.VM.Host] = true
		out = append(out, m)
	}
	return out
}

// renderSSHVMTargets is the execution-targets section for discovered VMs.
func renderSSHVMTargets(targets []vmMatch) string {
	if len(targets) == 0 {
		return ""
	}
	names := make([]string, 0, min(len(targets), maxListedVMTargets)+1)
	for i, m := range targets {
		if i == maxListedVMTargets {
			names = append(names, fmt.Sprintf("and %d more", len(targets)-maxListedVMTargets))
			break
		}
		names = append(names, fmt.Sprintf("%s (%s, via %s)", m.VM.label(), m.VM.Host, m.Config.Name))
	}
	return fmt.Sprintf("Discovered VMs reachable over SSH, most recently seen first: %s.\n%s",
		strings.Join(names, ", "),
		`server_command_executor: pass instance="<instance id, IP or name>"; the host is resolved from the VM.`)
}
