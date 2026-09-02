package integrations

import (
	"context"
	"fmt"
	"net/http"
	neturl "net/url"
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
	probe := &tableapi.TableRequestBuilder2GetRequestConfiguration{
		QueryParameters: &tableapi.TableRequestBuilder2GetQueryParameters{Limit: 1},
	}
	if _, err := client.Now().Table("incident").Get(context.Background(), probe); err != nil {
		return []error{interpretServiceNowError(err)}
	}

	return nil
}

const (
	serviceNowUserPageSize = 200
	serviceNowMaxPages     = 200 // safety cap → up to 40k users
)

// normalizeServiceNowURL prepares the user-entered instance URL for the SDK.
//
// Two things depend on this. The SDK validates with url.ParseRequestURI, which
// rejects a bare host like "acme.service-now.com" — the previous client built its
// own URL with an "https://" prefix and so accepted one, and tenants configured
// that way would otherwise stop connecting. And the SDK expands "{+baseurl}/api/now"
// as an RFC 6570 reserved expansion, so a trailing slash produces "//api/now".
//
// Leading/trailing whitespace is handled by the SDK's own WithURL, but trimming
// here too keeps the scheme check below correct.
func normalizeServiceNowURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := neturl.Parse(s)
	if err != nil || u.Host == "" {
		return s // let the SDK report the problem
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}

// newServiceNowClient builds a username/password ServiceNow client for the given
// instance. Shared by ValidateConfig and ListUsers. The SDK's default HTTP client
// has no timeout, so an unreachable instance would otherwise stall the caller —
// bind a hard per-request timeout here.
func newServiceNowClient(url, username, password string) (*servicenowsdkgo.ServiceNowServiceClient, error) {
	cred := credentials.NewBasicProvider(username, password)
	client, err := servicenowsdkgo.NewServiceNowServiceClient(
		servicenowsdkgo.WithAuthenticationProvider(cred),
		servicenowsdkgo.WithURL(normalizeServiceNowURL(url)),
		servicenowsdkgo.WithHTTPClient(&http.Client{Timeout: 20 * time.Second}),
	)
	if err != nil {
		return nil, fmt.Errorf("servicenow: failed to create client: %w", err)
	}
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
	rb := client.Now().Table("sys_user")

	var out []core.ExternalUser
	for page := 0; page < serviceNowMaxPages; page++ {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		resp, err := rb.Get(ctx, &tableapi.TableRequestBuilder2GetRequestConfiguration{
			QueryParameters: &tableapi.TableRequestBuilder2GetQueryParameters{
				Fields: []string{"sys_id", "user_name", "name", "email"},
				Query:  "active=true",
				Limit:  serviceNowUserPageSize,
				Offset: page * serviceNowUserPageSize,
			},
		})
		if err != nil {
			return nil, interpretServiceNowError(err)
		}
		if resp == nil {
			break
		}
		rows, err := resp.GetResult()
		if err != nil {
			return nil, interpretServiceNowError(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, e := range rows {
			if e == nil {
				continue
			}
			if u := mapServiceNowUser(e); u.ID != "" {
				out = append(out, u)
			}
		}
		if len(rows) < serviceNowUserPageSize {
			break
		}
	}
	return out, nil
}

// mapServiceNowUser converts one sys_user row to an ExternalUser. Pure (no I/O) so
// it's unit-testable without live credentials.
func mapServiceNowUser(e *tableapi.TableRecord) core.ExternalUser {
	return core.ExternalUser{
		ID:          serviceNowField(e, "sys_id"),
		Username:    serviceNowField(e, "user_name"),
		Email:       serviceNowField(e, "email"),
		DisplayName: serviceNowField(e, "name"),
	}
}

// serviceNowField reads a string field from a sys_user row, "" when absent.
//
// The HasAttribute check is load-bearing, not defensive padding: TableRecord.Get
// returns `&elem` where elem is a zero-value RecordElement when the key is absent,
// so the pointer is non-nil but its backing store is nil and GetValue panics on it.
// ServiceNow omits fields entirely for rows that have none, so an account without
// an email would otherwise take down the whole sync.
func serviceNowField(e *tableapi.TableRecord, key string) string {
	if e == nil || !e.HasAttribute(key) {
		return ""
	}
	element, err := e.Get(key)
	if err != nil || element == nil {
		return ""
	}
	v, err := element.GetValue()
	if err != nil {
		return ""
	}
	s, err := v.GetStringValue()
	if err != nil || s == nil {
		return ""
	}
	return strings.TrimSpace(*s)
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
