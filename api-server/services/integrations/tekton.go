package integrations

import (
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"regexp"
	"strings"
)

func init() {
	core.RegisterIntegration(Tekton{})
}

const IntegrationTekton = "tekton"

var k8sNamespaceRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

type Tekton struct{}

func (m Tekton) Name() string {
	return IntegrationTekton
}

func (m Tekton) Category() core.IntegrationCategory {
	return core.IntegrationCategoryCICD
}

func (m Tekton) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{},
		Properties: map[string]core.IntegrationSchemaProperty{
			"integration_config_name": {
				Type:        core.ToolSchemaTypeString,
				Description: "Name for Tekton Integration",
				Default:     "",
				Priority:    100,
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Accounts",
				Default:          nil,
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			"namespace": {
				Type:        core.ToolSchemaTypeString,
				Description: "Tekton namespace (lowercase alphanumeric/hyphens only; leave empty for all namespaces)",
				Default:     "",
				Priority:    80,
				IsTestable:  true,
			},
			"timeout": {
				Type:        core.ToolSchemaTypeString,
				Description: "Command timeout in seconds",
				Default:     "30",
				Priority:    10,
			},
		},
	}
}

func (m Tekton) ValidateConfig(securityContext *security.SecurityContext, configs []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string, len(configs))
	for _, c := range configs {
		configMap[c.Name] = c.Value
	}

	cmd := "tkn version"
	ns := configMap["namespace"]
	if ns != "" {
		if !k8sNamespaceRegex.MatchString(ns) || len(ns) > 63 {
			return []error{fmt.Errorf("invalid namespace %q: must be a valid Kubernetes namespace (lowercase alphanumeric/hyphens, up to 63 chars)", ns)}
		}
		cmd = fmt.Sprintf("tkn version -n %s", ns)
	}

	resp, err := relay.CommandExecutor(accountId, cmd, "", nil)
	if err != nil {
		return core.HandleRelayTimeoutError(err)
	}

	respStr, ok := resp["response"].(string)
	if !ok {
		return []error{fmt.Errorf("unexpected response format from tkn: %v", resp)}
	}

	if detectTektonNotInstalled(respStr) {
		return []error{fmt.Errorf("tekton validation failed: Tekton Pipelines does not appear to be installed in the cluster")}
	}

	if strings.Contains(respStr, "Pipeline version") || strings.Contains(respStr, "Pipelines version") {
		return nil
	}

	if errMsg := detectTektonError(respStr); errMsg != "" {
		return []error{fmt.Errorf("tekton validation failed: %s", errMsg)}
	}

	return []error{fmt.Errorf("tekton validation failed: unexpected response: %s", strings.TrimSpace(respStr))}
}

func detectTektonNotInstalled(resp string) bool {
	lower := strings.ToLower(resp)
	patterns := []string{
		"command not found",
		"executable file not found",
		"not installed",
		"the server doesn't have a resource type",
		"unable to resolve",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

func detectTektonError(resp string) string {
	lower := strings.ToLower(resp)
	patterns := []string{
		"error:",
		"failed:",
		"permission denied",
		"forbidden",
		"unauthorized",
		"connection refused",
		"no such host",
		"i/o timeout",
		"fata[",
		"erro[",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return strings.TrimSpace(resp)
		}
	}
	return ""
}
