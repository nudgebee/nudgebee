package integrations

import (
	"fmt"
	"nudgebee/services/integrations/core"
	"nudgebee/services/relay"
	"nudgebee/services/security"
	"strings"
)

func init() {
	core.RegisterIntegration(RabbitMq{})
}

const IntegrationRabbitMQ = "rabbitmq"

type RabbitMq struct {
}

func (m RabbitMq) Name() string {
	return IntegrationRabbitMQ
}

func (m RabbitMq) Category() core.IntegrationCategory {
	return core.IntegrationCategoryMessagingQueue
}

func (m RabbitMq) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{"k8s_secret", "host"},
		Properties: map[string]core.IntegrationSchemaProperty{
			"k8s_secret": {
				Type:        core.ToolSchemaTypeString,
				Description: "Rabbitmq Secret in k8s, Required Keys, RABBITMQ_HOST, RABBITMQ_PASSWORD, RABBITMQ_PORT, RABBITMQ_USER",
				IsTestable:  true,
			},
			"host": {
				Type:        core.ToolSchemaTypeString,
				Description: "rabbitmq host",
				IsTestable:  true,
			},
			"account_id": {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
			},
			"integration_config_name": {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of RabbitMq",
				Default:          "",
				AutoGenerateFunc: "",
			},
		},
	}
}

func (m RabbitMq) ValidateConfig(sc *security.SecurityContext, configs []core.IntegrationConfigValue, accountId string) []error {

	secretName := ""
	for _, integrationConfig := range configs {
		if strings.EqualFold(integrationConfig.Name, "k8s_secret") {
			secretName = integrationConfig.Value
			break
		}
	}

	if secretName == "" {
		return []error{fmt.Errorf("k8s_secret is required")}
	}

	// Use 'show overview' instead of 'list queues' - it always returns cluster info even with no queues
	command := "rabbitmqadmin --host $RABBITMQ_HOST --port $RABBITMQ_PORT --username $RABBITMQ_USER --password $RABBITMQ_PASSWORD show overview"
	resp, err := relay.CommandExecutor(accountId, command, secretName, map[string]string{})

	if err != nil {
		return core.HandleRelayError(err)
	}

	respStr, ok := resp["response"].(string)
	if !ok {
		return []error{fmt.Errorf("unexpected response format from rabbitmq server: %v", resp)}
	}

	respLower := strings.ToLower(respStr)

	// Check for specific RabbitMQ error patterns to provide actionable feedback
	switch {
	case strings.Contains(respLower, "access refused"):
		return []error{fmt.Errorf("authentication failed: invalid username or password")}
	case strings.Contains(respLower, "connection refused"):
		return []error{fmt.Errorf("connection refused: verify RABBITMQ_HOST and RABBITMQ_PORT are correct")}
	case strings.Contains(respLower, "not authorized") || strings.Contains(respLower, "not_allowed"):
		return []error{fmt.Errorf("authorization failed: user lacks required permissions for management API")}
	case strings.Contains(respLower, "name or service not known") || strings.Contains(respLower, "no such host"):
		return []error{fmt.Errorf("host not found: verify RABBITMQ_HOST is correct")}
	case strings.Contains(respLower, "timed out") || strings.Contains(respLower, "timeout"):
		return []error{fmt.Errorf("connection timed out: verify the RabbitMQ server is reachable")}
	}

	// Check for successful response - 'show overview' returns these fields on success
	if strings.Contains(respLower, "rabbitmq_version") ||
		strings.Contains(respLower, "cluster_name") ||
		strings.Contains(respLower, "management_version") {
		return nil
	}

	// Check for error indicators in the response
	if strings.Contains(respLower, "exit status") || strings.Contains(respLower, "error") {
		return []error{fmt.Errorf("rabbitmq validation failed: %s", respStr)}
	}

	return []error{fmt.Errorf("failed to validate rabbitmq connection - unexpected response: %s", respStr)}
}
