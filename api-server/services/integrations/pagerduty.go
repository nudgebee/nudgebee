package integrations

import (
	"context"
	"fmt"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/PagerDuty/go-pagerduty"
)

const (
	PagerDutyConfigUrl           = "url"
	PagerDutyConfigUsername      = "username"
	PagerDutyConfigPassword      = "password" // API key stored as password
	PagerDutyConfigAuthType      = "auth_type"
	PagerDutyConfigProjects      = "projects" // Services
	PagerDutyConfigLastConnected = "last_connected"
	PagerDutyConfigAllowComments = "allow_comments"
)

func init() {
	core.RegisterIntegration(PagerDuty{})
}

const IntegrationPagerDuty = "pagerduty"

type PagerDuty struct{}

func (p PagerDuty) Name() string {
	return IntegrationPagerDuty
}

func (p PagerDuty) Category() core.IntegrationCategory {
	return core.IntegrationCategoryTicketing
}

func (p PagerDuty) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{core.IntegrationConfigName, PagerDutyConfigPassword},
		Properties: map[string]core.IntegrationSchemaProperty{
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "A unique name to identify this PagerDuty account configuration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			PagerDutyConfigUrl: {
				Type:        core.ToolSchemaTypeString,
				Description: "PagerDuty instance URL (e.g., api.pagerduty.com)",
				Priority:    99,
				Default:     "api.pagerduty.com",
				AllowEdit:   false,
			},
			PagerDutyConfigUsername: {
				Type:        core.ToolSchemaTypeString,
				Description: "PagerDuty username or email",
				Priority:    98,
			},
			PagerDutyConfigPassword: {
				Type:        core.ToolSchemaTypeString,
				Description: "PagerDuty API key",
				IsEncrypted: true,
				Priority:    97,
			},
			PagerDutyConfigAuthType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication type (token or application)",
				Default:     "token",
				Hidden:      true,
			},
			PagerDutyConfigProjects: {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON array of PagerDuty services",
				Hidden:      true,
			},
			PagerDutyConfigLastConnected: {
				Type:        core.ToolSchemaTypeString,
				Description: "Last sync timestamp",
				Hidden:      true,
			},
			PagerDutyConfigAllowComments: {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Allow Comments",
				Priority:    96,
			},
		},
	}
}

func (p PagerDuty) ValidateConfig(ctx *security.SecurityContext, values []core.IntegrationConfigValue, accountId string) []error {
	apiKey := ""

	// Extract config values
	for _, config := range values {
		if config.Name == PagerDutyConfigPassword {
			apiKey = config.Value
		}
	}

	// Validate required fields
	if apiKey == "" {
		return []error{fmt.Errorf("pagerduty api key is required")}
	}

	// Create PagerDuty client and test connection
	client := pagerduty.NewClient(apiKey)

	// Test by listing services (limit to 1 for validation)
	_, err := client.ListServicesWithContext(context.Background(), pagerduty.ListServiceOptions{
		Limit: 1,
	})
	if err != nil {
		return []error{fmt.Errorf("pagerduty authentication failed: %w", err)}
	}

	return nil
}
