package integrations

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	client, err := newServiceNowClient(url, username, password)
	if err != nil {
		return []error{err}
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

const (
	serviceNowUserPageSize = 200
	serviceNowMaxPages     = 200 // safety cap → up to 40k users
)

// newServiceNowClient builds a username/password ServiceNow client for the given
// instance. Shared by ValidateConfig and ListUsers. The SDK's default HTTP session
// has no timeout and its Get() ignores the context, so an unreachable instance
// would otherwise stall the caller — bind a hard per-request timeout here.
func newServiceNowClient(url, username, password string) (*servicenowsdkgo.ServiceNowClient, error) {
	cred := credentials.NewUsernamePasswordCredential(username, password)
	client, err := servicenowsdkgo.NewServiceNowClient2(cred, url)
	if err != nil {
		return nil, fmt.Errorf("servicenow: failed to create client: %w", err)
	}
	client.Session = &http.Client{Timeout: 20 * time.Second}
	return client, nil
}

// ListUsers enumerates ServiceNow users (sys_user table) for identity sync. Active
// users only; sys_user always carries an email, so accounts auto-match by email.
// Implements core.UserLister.
func (s ServiceNow) ListUsers(ctx context.Context, values []core.IntegrationConfigValue) ([]core.ExternalUser, error) {
	url := core.ConfigValue(values, ServiceNowConfigUrl)
	username := core.ConfigValue(values, ServiceNowConfigUsername)
	password := core.ConfigValue(values, ServiceNowConfigPassword)
	if url == "" || username == "" || password == "" {
		return nil, fmt.Errorf("servicenow: url, username and password are required")
	}

	client, err := newServiceNowClient(url, username, password)
	if err != nil {
		return nil, err
	}
	baseURL := fmt.Sprintf("https://%s/api/now", strings.TrimPrefix(url, "https://"))
	rb := tableapi.NewTableRequestBuilder(client, map[string]string{"baseurl": baseURL, "table": "sys_user"})

	var out []core.ExternalUser
	for page := 0; page < serviceNowMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		resp, err := rb.Get(&tableapi.TableRequestBuilderGetQueryParameters{
			Fields: []string{"sys_id", "user_name", "name", "email"},
			Query:  "active=true",
			Limit:  serviceNowUserPageSize,
			Offset: page * serviceNowUserPageSize,
		})
		if err != nil {
			return nil, interpretServiceNowError(err)
		}
		if resp == nil || len(resp.Result) == 0 {
			break
		}
		for _, e := range resp.Result {
			if e == nil {
				continue
			}
			if u := mapServiceNowUser(e); u.ID != "" {
				out = append(out, u)
			}
		}
		if len(resp.Result) < serviceNowUserPageSize {
			break
		}
	}
	return out, nil
}

// mapServiceNowUser converts one sys_user row to an ExternalUser. Pure (no I/O) so
// it's unit-testable without live credentials.
func mapServiceNowUser(e *tableapi.TableEntry) core.ExternalUser {
	return core.ExternalUser{
		ID:          serviceNowField(e, "sys_id"),
		Username:    serviceNowField(e, "user_name"),
		Email:       serviceNowField(e, "email"),
		DisplayName: serviceNowField(e, "name"),
	}
}

// serviceNowField reads a string field from a sys_user row, "" when absent.
func serviceNowField(e *tableapi.TableEntry, key string) string {
	v := e.Value(key)
	if v == nil {
		return ""
	}
	s, err := v.String()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(s)
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
