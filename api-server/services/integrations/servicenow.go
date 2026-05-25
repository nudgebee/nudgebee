package integrations

import (
	"fmt"
	"strings"

	servicenowsdkgo "github.com/michaeldcanady/servicenow-sdk-go"
	"github.com/michaeldcanady/servicenow-sdk-go/credentials"
	tableapi "github.com/michaeldcanady/servicenow-sdk-go/table-api"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

const (
	ServiceNowConfigUrl               = "url"
	ServiceNowConfigUsername          = "username"
	ServiceNowConfigPassword          = "password"
	ServiceNowConfigAuthType          = "auth_type"
	ServiceNowConfigProjects          = "projects"
	ServiceNowConfigLastConnected     = "last_connected"
	ServiceNowConfigSyncKnowledgeBase = "sync_knowledge_base"
)

func init() {
	core.RegisterIntegration(ServiceNow{})
}

const IntegrationServiceNow = "servicenow"

type ServiceNow struct{}

func (s ServiceNow) Name() string {
	return IntegrationServiceNow
}

func (s ServiceNow) Category() core.IntegrationCategory {
	return core.IntegrationCategoryTicketing
}

func (s ServiceNow) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{ServiceNowConfigUrl, ServiceNowConfigUsername, ServiceNowConfigPassword},
		Properties: map[string]core.IntegrationSchemaProperty{
			ServiceNowConfigUrl: {
				Type:        core.ToolSchemaTypeString,
				Description: "ServiceNow instance URL (e.g., instance.service-now.com)",
			},
			ServiceNowConfigUsername: {
				Type:        core.ToolSchemaTypeString,
				Description: "ServiceNow username",
			},
			ServiceNowConfigPassword: {
				Type:        core.ToolSchemaTypeString,
				Description: "ServiceNow password",
				IsEncrypted: true,
			},
			ServiceNowConfigAuthType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Authentication type (token or application)",
				Default:     "token",
			},
			ServiceNowConfigProjects: {
				Type:        core.ToolSchemaTypeString,
				Description: "JSON array of ServiceNow tables (e.g., incident)",
			},
			ServiceNowConfigLastConnected: {
				Type:        core.ToolSchemaTypeString,
				Description: "Last sync timestamp",
			},
			ServiceNowConfigSyncKnowledgeBase: {
				Type:        core.ToolSchemaTypeBoolean,
				Description: "Enable syncing of ServiceNow knowledge base",
			},
		},
	}
}

func (s ServiceNow) ValidateConfig(ctx *security.SecurityContext, values []core.IntegrationConfigValue, accountId string) []error {
	url := ""
	username := ""
	password := ""

	// Extract config values
	for _, config := range values {
		switch config.Name {
		case ServiceNowConfigUrl:
			url = config.Value
		case ServiceNowConfigUsername:
			username = config.Value
		case ServiceNowConfigPassword:
			password = config.Value
		}
	}

	// Validate required fields
	if url == "" {
		return []error{fmt.Errorf("servicenow url is required")}
	}
	if username == "" {
		return []error{fmt.Errorf("servicenow username is required")}
	}
	if password == "" {
		return []error{fmt.Errorf("servicenow password is required")}
	}

	// Create ServiceNow client
	cred := credentials.NewUsernamePasswordCredential(username, password)
	client, err := servicenowsdkgo.NewServiceNowClient2(cred, url)
	if err != nil {
		return []error{fmt.Errorf("failed to create servicenow client: %w", err)}
	}

	// Test connection by querying incident table
	baseURL := fmt.Sprintf("https://%s/api/now", strings.TrimPrefix(url, "https://"))
	requestBuilder := tableapi.NewTableRequestBuilder(client, map[string]string{
		"baseurl": baseURL,
		"table":   "incident",
	})

	if _, err := requestBuilder.Get(&tableapi.TableRequestBuilderGetQueryParameters{Limit: 1}); err != nil {
		return []error{interpretServiceNowError(err)}
	}

	return nil
}

// interpretServiceNowError translates the SDK's terse "no error factory is
// registered for this code: <N>" message into a user-actionable one. Falls
// back to the raw error when the status code isn't recognized.
func interpretServiceNowError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "401"):
		return fmt.Errorf("servicenow authentication failed: invalid username or password")
	case strings.Contains(msg, "403"):
		return fmt.Errorf("servicenow authentication failed: user lacks permission to read the incident table")
	case strings.Contains(msg, "404"):
		return fmt.Errorf("servicenow connection failed: instance URL not found (check the URL field)")
	default:
		return fmt.Errorf("servicenow auth failed: %w", err)
	}
}
