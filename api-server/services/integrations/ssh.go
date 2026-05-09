package integrations

import (
	"errors"
	"fmt"
	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strings"
)

const (
	IntegrationSSH = "ssh"
)

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
				Priority:    100,
				IsTestable:  true,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Ssh",
				Default:          "",
				AutoGenerateFunc: "",
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
			"host": {
				Type:         core.ToolSchemaTypeString,
				Description:  "Server Host",
				ShowWhen:     map[string]any{"connection_mode": "k8s"},
				RequiredWhen: map[string]any{"connection_mode": "k8s"},
				Priority:     75,
				IsTestable:   true,
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
			"username": {
				Type:        core.ToolSchemaTypeString,
				Description: "SSH username",
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
		if configMap["username"] == "" {
			errs = append(errs, fmt.Errorf("username is required for cloud_push credentials"))
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

	user := "$SSH_USER"
	host := "$SSH_HOST"

	if params.UserName != "" {
		user = params.UserName
	}
	if params.HostName != "" {
		host = params.HostName
	}

	userAndHost := fmt.Sprintf("%s@%s", user, host)

	finalCommand := fmt.Sprintf(`mkdir -p ~/.ssh && echo "$SSH_KEY" > ~/.ssh/id_rsa && chmod 600 ~/.ssh/id_rsa && ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR %s "%s"`, userAndHost, params.Command)

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
