package integrations

import (
	"errors"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"strings"
)

const (
	IntegrationSSH = "ssh"
)

// sshHostPattern matches an RFC 1123 hostname or an IPv4 address.
// IPv6 is intentionally excluded: SSH integrations in this codebase target
// hostnames or IPv4; IPv6 can be added behind a flag if a user need surfaces.
//
// sshUserPattern matches a POSIX-ish username (starts with a letter or
// underscore, then alphanumerics / dot / underscore / hyphen). Both regexes
// gate values before they're interpolated into the `ssh user@host` shell
// template constructed in executeInternal — without this gate, shell
// metacharacters in playbook params or LLM tool args would smuggle commands
// past the SSH boundary. Patterns mirror the duplicates in
// relay-server/pkg/server/handlers/workspace.go and
// llm/llm-server/tools/common_relay.go; keep all three in sync.
const sshHostPattern = `^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`
const sshUserPattern = `^[A-Za-z_][A-Za-z0-9._-]{0,31}$`

var sshHostRegex = regexp.MustCompile(sshHostPattern)
var sshUserRegex = regexp.MustCompile(sshUserPattern)

func init() {
	core.RegisterIntegration(SSH{})
	playbooks.RegisterAction(IntegrationSSH, &SSH{})
}

type SSH struct {
}

type sshParams struct {
	Command         string `json:"command,omitempty"`
	IntegrationName string `json:"integration_name,omitempty"`
	HostName        string `json:"host_name,omitempty"`
	UserName        string `json:"user_name,omitempty"`
	AccountId       string `json:"account_id,omitempty"`
}

func (m SSH) Name() string {
	return IntegrationSSH
}

func (m SSH) Category() core.IntegrationCategory {
	return core.IntegrationCategoryDatabase
}

func (m SSH) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"connection_mode": {
				Type:        core.ToolSchemaTypeString,
				Description: "Connection mode",
				Default:     "k8s",
				Enum:        []any{"k8s", "vm_agent"},
				Priority:    92,
				IsTestable:  true,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Ssh",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			// K8s fields
			"k8s_secret": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Kubernetes secret containing SSH_KEY, SSH_HOST, SSH_USER keys",
				ShowWhen:     map[string]any{"connection_mode": "k8s"},
				RequiredWhen: map[string]any{"connection_mode": "k8s"},
				Priority:     80,
				IsTestable:   true,
			},
			// Resolution order at execute time (kept in code, not the UI string,
			// because the form renders Description as helper text and a
			// multi-line ladder looks like a wall of words):
			//   (1) per-command host_name in the tool/playbook args
			//   (2) this field if set
			//   (3) SSH_HOST entry in the referenced Kubernetes secret
			// If none are present the command fails. Leave blank for
			// ephemeral targets (a lab VM whose IP rotates per session).
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "Default server host (e.g. db.example.com or 10.0.0.5). Optional — if blank, callers must supply host_name per command, or SSH_HOST from the k8s secret is used.",
				Pattern:     sshHostPattern,
				ShowWhen:    map[string]any{"connection_mode": "k8s"},
				Priority:    75,
				IsTestable:  true,
			},
			// VM agent fields
			"credential_source": {
				Type:        core.ToolSchemaTypeString,
				Description: "Where SSH credentials are stored",
				Default:     "cloud_push",
				Enum:        []any{"cloud_push", "aws_sm", "gcp_sm", "azure_kv", "local"},
				ShowWhen:    map[string]any{"connection_mode": "vm_agent"},
				Priority:    60,
				IsTestable:  true,
			},
			// Resolution order at execute time:
			//   (1) per-command user_name in the tool/playbook args
			//   (2) this field if set
			// If neither is present the command fails.
			"username": {
				Type:        core.ToolSchemaTypeString,
				Description: "Default SSH username. Optional — callers may override per command via user_name.",
				Pattern:     sshUserPattern,
				ShowWhen:    map[string]any{"connection_mode": "vm_agent", "credential_source": "cloud_push"},
				Priority:    50,
				IsTestable:  true,
			},
			"private_key": {
				Type:        core.ToolSchemaTypeString,
				Description: "SSH private key (PEM format)",
				IsEncrypted: true,
				ShowWhen:    map[string]any{"connection_mode": "vm_agent", "credential_source": "cloud_push"},
				Priority:    45,
				IsTestable:  true,
			},
			"password": {
				Type:        core.ToolSchemaTypeString,
				Description: "SSH password (if not using private key)",
				IsEncrypted: true,
				ShowWhen:    map[string]any{"connection_mode": "vm_agent", "credential_source": "cloud_push"},
				Priority:    40,
				IsTestable:  true,
			},
			"passphrase": {
				Type:        core.ToolSchemaTypeString,
				Description: "Passphrase for the private key (if encrypted)",
				IsEncrypted: true,
				ShowWhen:    map[string]any{"connection_mode": "vm_agent", "credential_source": "cloud_push"},
				Priority:    35,
				IsTestable:  true,
			},
			"secret_ref": {
				Type:        core.ToolSchemaTypeString,
				Description: "Secret reference in the secret manager",
				ShowWhen:    map[string]any{"credential_source": []any{"aws_sm", "gcp_sm", "azure_kv"}},
				Priority:    55,
				IsTestable:  true,
			},
		},
	}
}

func (m SSH) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range config {
		configMap[c.Name] = c.Value
	}

	connectionMode := configMap["connection_mode"]
	if connectionMode == "vm_agent" {
		return m.validateVMAgentConfig(configMap)
	}

	// k8s_secret is the only credential source the executor knows how to
	// read in k8s mode (executeInternal returns "k8s_secret not found" if
	// absent). The schema marks it RequiredWhen, but enforce it here too
	// so a save that bypasses the form-level RequiredWhen check (e.g. a
	// direct API caller) still fails fast with a clear error rather than
	// deferring to a confusing runtime failure.
	if strings.TrimSpace(configMap["k8s_secret"]) == "" {
		return []error{fmt.Errorf("k8s_secret is required for k8s connection mode")}
	}

	host := strings.TrimSpace(configMap["host"])
	if host == "" {
		// Empty host is permitted: the integration is being saved as a
		// credential-only default, with the actual host supplied per-call
		// by the caller (e.g. an LLM tool extracting it from a user query).
		// Skip the format check and the live uname -a probe.
		return []error{}
	}
	if !sshHostRegex.MatchString(host) {
		return []error{fmt.Errorf("invalid host %q: must be a hostname (e.g. db.example.com) or IPv4 address (e.g. 10.0.0.5)", host)}
	}

	// K8s mode: validate by running a test command
	_, err := m.executeInternal(accountId, config, sshParams{
		Command: "uname -a",
	})
	if err != nil {
		return []error{err}
	}
	return []error{}
}

func (m SSH) validateVMAgentConfig(configMap map[string]string) []error {
	var errs []error
	credSource := configMap["credential_source"]
	if credSource == "" || credSource == "cloud_push" {
		// username is no longer required at save time: it can be supplied per-call
		// by the caller (e.g. an LLM tool) alongside host_name for ephemeral targets.
		// When it IS provided, vet the format so a malformed default can't smuggle
		// shell metacharacters through to executeInternal's command construction.
		if u := strings.TrimSpace(configMap["username"]); u != "" && !sshUserRegex.MatchString(u) {
			errs = append(errs, fmt.Errorf("invalid username %q: must start with a letter or underscore and contain only alphanumerics, dot, underscore, hyphen", u))
		}
		if configMap["password"] == "" && configMap["private_key"] == "" {
			errs = append(errs, fmt.Errorf("either password or private_key is required for cloud_push credentials"))
		}
	}
	if credSource == "aws_sm" || credSource == "gcp_sm" || credSource == "azure_kv" {
		if configMap["secret_ref"] == "" {
			errs = append(errs, fmt.Errorf("secret_ref is required for %s credential source", credSource))
		}
	}
	return errs
}

func (m SSH) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	var params sshParams
	err := common.UnmarshalMapToStruct(rawParams, &params)
	if err != nil {
		return nil, err
	}

	if params.Command == "" {
		return nil, errors.New("command is required")
	}

	if params.IntegrationName == "" {
		return nil, errors.New("integration_name is required")
	}

	if params.AccountId == "" {
		params.AccountId = ctx.GetAccountId()
	}

	if params.AccountId == "" {
		return nil, errors.New("account_id is required")
	}

	requestContext := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)
	integrations, err := core.ListIntegrationConfigs(requestContext, params.AccountId, IntegrationSSH)
	if err != nil {
		return nil, err
	}
	var integration core.IntegrationDto
	for _, intg := range integrations {
		if strings.EqualFold(intg.Name, params.IntegrationName) {
			integration = intg
			break
		}
	}

	if integration.Name == "" {
		return nil, errors.New("integration not found")
	}

	resp, err := m.executeInternal(params.AccountId, integration.Configs, params)
	if err != nil {
		return nil, err
	}

	metadata := map[string]any{
		"query-result-version": "1.0",
		"query":                rawParams,
	}
	return playbooks.NewPlaybookActionResponseJson(resp, map[string]any{}, []playbooks.PlaybookActionResponseInsight{}, metadata), err
}

// resolveSSHTarget computes the user@host pair that executeInternal
// interpolates into the generated `ssh user@host "<cmd>"` shell template.
// Resolution order (lowest precedence first):
//
//	(3) "$SSH_USER" / "$SSH_HOST" — env-var defaults from the mounted k8s secret
//	(2) integration's saved host / username
//	(1) per-call HostName / UserName in sshParams
//
// Every value that lands in the shell command — saved OR per-call — is
// regex-vetted before substitution. Per-call params are the security gate:
// playbook callers and LLM tool args reach here unsanitised. Saved values
// are re-validated as defense in depth (ValidateConfig already vets at save
// time, but the executor must not assume that).
//
// Returns "" / "" / err if any input fails its format check; the caller
// must not interpolate the partial pair.
//
// The function is pure (no I/O), so the resolution and validation logic is
// directly testable without a relay client.
func resolveSSHTarget(configs []core.IntegrationConfigValue, params sshParams) (string, string, error) {
	savedHost := ""
	savedUser := ""
	for _, c := range configs {
		switch strings.ToLower(c.Name) {
		case "host":
			savedHost = strings.TrimSpace(c.Value)
		case "username":
			savedUser = strings.TrimSpace(c.Value)
		}
	}

	user := "$SSH_USER"
	host := "$SSH_HOST"
	if savedHost != "" {
		if !sshHostRegex.MatchString(savedHost) {
			return "", "", fmt.Errorf("invalid saved host %q in integration config", savedHost)
		}
		host = savedHost
	}
	if savedUser != "" {
		if !sshUserRegex.MatchString(savedUser) {
			return "", "", fmt.Errorf("invalid saved username %q in integration config", savedUser)
		}
		user = savedUser
	}
	if params.HostName != "" {
		if !sshHostRegex.MatchString(params.HostName) {
			return "", "", fmt.Errorf("invalid host_name %q: must be a hostname or IPv4 address", params.HostName)
		}
		host = params.HostName
	}
	if params.UserName != "" {
		if !sshUserRegex.MatchString(params.UserName) {
			return "", "", fmt.Errorf("invalid user_name %q: must start with a letter or underscore and contain only alphanumerics, dot, underscore, hyphen", params.UserName)
		}
		user = params.UserName
	}
	return user, host, nil
}

// sshShellQuote returns a single-quoted shell-safe encoding of s, with any
// embedded single quote broken out via close-quote / escaped-quote /
// reopen-quote — the standard POSIX shell-quoting pattern. Used to
// stop the workspace pod's local shell from re-parsing the user's command
// (variable / positional-param / `$(...)` expansion) before ssh transmits it.
//
// Mirrors the same helper in
//   - llm/llm-server/tools/common_relay.go
//   - collector-server/.../workspace.go
//
// Keep all three in sync.
func sshShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (m SSH) executeInternal(accountId string, configs []core.IntegrationConfigValue, params sshParams) (map[string]any, error) {
	secretName := ""
	for _, integrationConfig := range configs {
		if strings.EqualFold(integrationConfig.Name, "k8s_secret") {
			secretName = integrationConfig.Value
			break
		}
	}

	if secretName == "" {
		return nil, errors.New("k8s_secret not found")
	}

	user, host, err := resolveSSHTarget(configs, params)
	if err != nil {
		return nil, err
	}

	userAndHost := fmt.Sprintf("%s@%s", user, host)

	// NOTE: This `mkdir / echo $SSH_KEY / ssh user@host '<cmd>'` template is
	// also constructed in two other places that share the same secret
	// contract — keep all three in sync if you change the format:
	//   - llm/llm-server/tools/common_relay.go         (RelayJobSSH case)
	//   - collector-server/.../workspace.go (case "ssh" in buildWorkspaceAction)
	//
	// `sshShellQuote` single-quotes `params.Command` so the workspace pod's
	// local shell can't re-parse it before ssh transmits it (otherwise `$VAR`,
	// `$1`, `$(...)`, backticks inside the user's command get expanded
	// locally rather than on the remote host).
	//
	// `-o ConnectTimeout=10` makes unreachable-host failures (wrong IP, port
	// firewalled) surface within 10s; without it the only signal is the 60s
	// pod-execution timeout, which looks like "command hung" to the LLM and
	// triggers expensive retry loops.
	finalCommand := fmt.Sprintf(`mkdir -p ~/.ssh && echo "$SSH_KEY" > ~/.ssh/id_rsa && chmod 600 ~/.ssh/id_rsa && ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR -o ConnectTimeout=10 %s %s`, userAndHost, sshShellQuote(params.Command))

	cliResp, err := relay.CommandExecutor(accountId, finalCommand, secretName, map[string]string{})

	if err != nil {
		return nil, err
	}

	resp := map[string]any{
		"command": params.Command,
		"stdout":  cliResp["response"],
	}
	return resp, err
}
