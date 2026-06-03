package integrations

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
)

const (
	Last9ConfigEndpoint = "endpoint"
	Last9ConfigUsername = "username"
	Last9ConfigPassword = "password"
)

const IntegrationLast9 = "last9"

type Last9 struct {
}

func init() {
	core.RegisterIntegration(Last9{})
}

func (m Last9) Name() string {
	return IntegrationLast9
}

func (m Last9) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m Last9) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{Last9ConfigEndpoint, Last9ConfigUsername, Last9ConfigPassword},
		Properties: map[string]core.IntegrationSchemaProperty{
			Last9ConfigEndpoint: {
				Type:        core.ToolSchemaTypeString,
				Description: "Last9 OTLP Endpoint (e.g. https://otlp.last9.io)",
				IsTestable:  true,
			},
			Last9ConfigUsername: {
				Type:        core.ToolSchemaTypeString,
				Description: "Last9 Cluster ID (Username)",
				IsTestable:  true,
			},
			Last9ConfigPassword: {
				Type:        core.ToolSchemaTypeString,
				Description: "Last9 Write Token (Password)",
				IsEncrypted: true,
				IsTestable:  true,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Accounts",
				Default:          nil,
				AutoGenerateFunc: "listAccounts",
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name of Last9 Integration",
				Default:          "",
				AutoGenerateFunc: "",
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Last9 default Log Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
			core.DefaultTraceProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Last9 default Trace Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
			core.DefaultMetricsProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Last9 default Metrics Provider",
				Default:          false,
				AutoGenerateFunc: "",
			},
		},
	}
}

func (m *Last9) ValidateLast9Config(endpoint, username, password string) error {
	if endpoint == "" {
		return errors.New("endpoint must not be empty")
	}
	if username == "" {
		return errors.New("username (cluster ID) must not be empty")
	}
	if password == "" {
		return errors.New("password (write token) must not be empty")
	}

	// Validate by hitting the OTLP endpoint with Basic Auth
	authToken := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	headers := map[string]string{
		"Authorization": "Basic " + authToken,
	}

	res, err := common.HttpGet(endpoint, common.HttpWithHeaders(headers))
	if err != nil {
		return fmt.Errorf("failed to validate Last9 connection: %w", err)
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			fmt.Printf("error closing response body: %v\n", err)
		}
	}()

	// Accept 200, 405 (Method Not Allowed - OTLP endpoints typically only accept POST), or 400 as valid
	// A 401/403 means invalid credentials
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return fmt.Errorf("Last9 authentication failed: status=%d", res.StatusCode)
	}
	return nil
}

func (m Last9) ValidateConfig(securityContext *security.SecurityContext, integrationConfig []core.IntegrationConfigValue, accountId string) []error {
	endpoint := ""
	username := ""
	password := ""
	var errs []error
	for _, config := range integrationConfig {
		switch config.Name {
		case Last9ConfigEndpoint:
			endpoint = config.Value
		case Last9ConfigUsername:
			username = config.Value
		case Last9ConfigPassword:
			password = config.Value
			if config.IsEncrypted {
				var err error
				password, err = common.Decrypt(config.Value)
				if err != nil {
					return []error{fmt.Errorf("last9 config validation failed: %w", err)}
				}
			}
		}
	}

	if err := m.ValidateLast9Config(endpoint, username, password); err != nil {
		errs = []error{fmt.Errorf("last9 config validation failed: %w", err)}
	}
	return errs
}

func GetLast9Configs(sc *security.RequestContext, accountId string) (string, string, string, error) {
	endpoint := ""
	username := ""
	password := ""

	last9Integrations, err := core.ListIntegrationConfigs(sc, accountId, IntegrationLast9)
	if err != nil {
		return endpoint, username, password, fmt.Errorf("failed to list last9 integration configs: %w", err)
	}
	if len(last9Integrations) == 0 {
		return endpoint, username, password, fmt.Errorf("last9 integration not found for account: %s", accountId)
	}
	last9Integration := last9Integrations[0]
	for _, config := range last9Integration.Configs {
		switch config.Name {
		case Last9ConfigEndpoint:
			endpoint = config.Value
		case Last9ConfigUsername:
			username = config.Value
		case Last9ConfigPassword:
			password = config.Value
			if config.IsEncrypted {
				var err error
				password, err = common.Decrypt(config.Value)
				if err != nil {
					return endpoint, username, password, fmt.Errorf("failed to decrypt last9 password: %w", err)
				}
			}
		}
	}

	return endpoint, username, password, nil
}
