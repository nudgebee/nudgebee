package integrations

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"time"
)

const (
	ObserveConfigUserEmail    = "user_email"
	ObserveConfigUserPassword = "user_password"
	ObserveApiToken           = "api_token"
	ObserveConfigCustomerID   = "customer_id"
	ObserveConfigLogDatasetID = "log_dataset_id"
	ObserveConfigDomain       = "domain"
)

func init() {
	core.RegisterIntegration(Observe{})
}

const IntegrationObserve = "observe"

type ObserveConfig struct {
	UserEmail    string `json:"user_email"`
	UserPassword string `json:"user_password"`
	CustomerID   string `json:"customer_id"`
	LogDatasetID string `json:"log_dataset_id"`
	ApiToken     string `json:"api_token"`
	Domain       string `json:"domain"`
}

type Observe struct {
}

func (m Observe) Name() string {
	return IntegrationObserve
}

func (m Observe) Category() core.IntegrationCategory {
	return core.IntegrationCategoryObservabilityPlatform
}

func (m Observe) BaseURL(config ObserveConfig) string {
	return fmt.Sprintf("https://%s.%s.com", config.CustomerID, config.Domain)
}

type TokenResponse struct {
	Token string `json:"access_key"`
	OK    bool   `json:"ok"`
}

type ObserveInput struct {
	InputName string `json:"inputName"`
	DatasetId string `json:"datasetId"`
}

type ObserveStage struct {
	Input    []ObserveInput `json:"input"`
	StageID  string         `json:"stageID"`
	Pipeline string         `json:"pipeline"`
}

type ObserveQuery struct {
	OutputStage string         `json:"outputStage"`
	Stages      []ObserveStage `json:"stages"`
}

type ObserveLogApiRequest struct {
	Query    ObserveQuery `json:"query"`
	RowCount string       `json:"rowCount"`
}

type ObserveLoginRequest struct {
	UserEmail    string `json:"user_email"`
	UserPassword string `json:"user_password"`
	TokenName    string `json:"tokenName"`
}

func (m Observe) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Testable: true,
		Required: []string{ObserveConfigCustomerID, ObserveConfigLogDatasetID},
		Properties: map[string]core.IntegrationSchemaProperty{
			ObserveApiToken: {
				Type:        core.ToolSchemaTypeString,
				Description: "Observe Api Token",
				IsEncrypted: true,
				IsTestable:  true,
				RequiredWhen: map[string]any{
					core.AuthType: []string{ObserveApiToken},
				},
				Priority: 98,
			},
			ObserveConfigUserEmail: {
				Type:        core.ToolSchemaTypeString,
				Description: "Observe User Email",
				IsTestable:  true,
				RequiredWhen: map[string]any{
					core.AuthType: []string{"email_password"},
				},
				Priority: 97,
			},
			ObserveConfigUserPassword: {
				Type:        core.ToolSchemaTypeString,
				Description: "Observe User Password",
				IsEncrypted: true,
				IsTestable:  true,
				RequiredWhen: map[string]any{
					core.AuthType: []string{"email_password"},
				},
				Priority: 96,
			},
			ObserveConfigCustomerID: {
				Type:        core.ToolSchemaTypeString,
				Description: "Observe Customer ID",
				Priority:    95,
				IsTestable:  true,
			},
			ObserveConfigLogDatasetID: {
				Type:        core.ToolSchemaTypeString,
				Description: "Observe Log Dataset ID",
				Priority:    94,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Select Account",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         100,
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make Observe Default Log Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         93,
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Name To Observe Integration",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         92,
			},
			core.AuthType: {
				Type:        core.ToolSchemaTypeString,
				Description: "Select Authentication Type",
				Enum:        []any{"api_token", "email_password"},
				Priority:    99,
				IsTestable:  true,
			},
			ObserveConfigDomain: {
				Type:        core.ToolSchemaTypeString,
				Description: "Observe Domain (e.g. observeinc)",
				Enum:        []any{"ap-1.observeinc", "au-1.observeinc", "ca-1.observeinc", "eu-1.observeinc", "observeinc"},
				Priority:    91,
				Default:     "observeinc",
				IsTestable:  true,
			},
		},
	}
}

func (m *Observe) TestAuthToken(cfg ObserveConfig) error {
	executeQueryURL := fmt.Sprintf("%s/v1/meta/export/query", m.BaseURL(cfg))
	logApiRequest := ObserveLogApiRequest{
		Query: ObserveQuery{
			OutputStage: "output_stage",
			Stages: []ObserveStage{
				{
					Input: []ObserveInput{
						{
							InputName: "main",
							DatasetId: cfg.LogDatasetID,
						},
					},
					StageID:  "output_stage",
					Pipeline: "",
				},
			},
		},
		RowCount: "10",
	}
	params := map[string]string{
		"startTime": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		"endTime":   time.Now().UTC().Format(time.RFC3339)}
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/x-ndjson",
		"Authorization": fmt.Sprintf("Bearer %s %s", cfg.CustomerID, cfg.ApiToken),
	}
	jsonBody := common.HttpWithJsonBody(logApiRequest)
	res, err := common.HttpPost(executeQueryURL, common.HttpWithHeaders(headers), common.HttpWithQueryParams(params), jsonBody)
	if err != nil {
		return fmt.Errorf("failed to create execute query request: %w", err)
	}
	defer func() {
		if res != nil && res.Body != nil {
			if err := res.Body.Close(); err != nil {
				fmt.Printf("error closing response body: %v\n", err)
			}
		}
	}()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("execute query failed: status=%d body=%s", res.StatusCode, string(body))
	}
	return nil
}

func (m *Observe) ValidateObserveConfig(cfg ObserveConfig) error {
	if (cfg.UserEmail == "" || cfg.UserPassword == "") && cfg.ApiToken == "" {
		return errors.New("(email and password) or API token must not be empty")
	}
	if cfg.ApiToken != "" {
		return m.TestAuthToken(cfg)
	}

	loginURL := fmt.Sprintf("%s/auth/local/login", m.BaseURL(cfg))

	form := url.Values{}
	form.Set("email", cfg.UserEmail)
	form.Set("password", cfg.UserPassword)

	params := map[string]string{
		"email":    cfg.UserEmail,
		"password": cfg.UserPassword,
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}

	res, err := common.HttpPost(loginURL, common.HttpWithHeaders(headers), common.HttpWithQueryParams(params))
	defer func() {
		if err := res.Body.Close(); err != nil {
			fmt.Printf("error closing response body: %v\n", err)
		}
	}()

	if err != nil {
		return fmt.Errorf("failed to create login request: %w", err)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed: status=%d body=%s", res.StatusCode, string(body))
	}
	return nil
}

func (m *Observe) GetToken(cfg ObserveConfig) (string, error) {
	if (cfg.UserEmail == "" || cfg.UserPassword == "") && cfg.ApiToken == "" {
		return "", errors.New("email and password or api token must not be empty")
	}
	if cfg.ApiToken != "" {
		return cfg.ApiToken, nil
	}

	loginURL := fmt.Sprintf("%s/v1/login", m.BaseURL(cfg))

	LoginReq := ObserveLoginRequest{
		UserEmail:    cfg.UserEmail,
		UserPassword: cfg.UserPassword,
		TokenName:    "token_name",
	}

	res, err := common.HttpPost(loginURL, common.HttpWithHeaders(map[string]string{
		"Content-Type": "application/json",
	}), common.HttpWithJsonBody(LoginReq))
	if err != nil {
		return "", fmt.Errorf("failed to execute login request: %w", err)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("login failed: status=%d body=%s", res.StatusCode, string(body))
	}

	var tokenResp TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}
	if tokenResp.Token == "" {
		return "", errors.New("empty token received from server")
	}

	return tokenResp.Token, nil
}

func GetObserveConfigs(sc *security.RequestContext, accountId string) (ObserveConfig, error) {
	userEmail := ""
	userPassword := ""
	customerID := ""
	apiToken := ""
	logDatasetID := ""
	domain := ""

	observeIntegrations, err := core.ListIntegrationConfigs(sc, accountId, IntegrationObserve)
	if err != nil {
		return ObserveConfig{}, fmt.Errorf("failed to list observe integration configs: %w", err)
	}
	if len(observeIntegrations) == 0 {
		return ObserveConfig{}, fmt.Errorf("observe integration not found for account: %s", accountId)
	}
	observeIntegration := observeIntegrations[0]
	for _, config := range observeIntegration.Configs {
		switch config.Name {
		case ObserveConfigUserEmail:
			userEmail = config.Value
		case ObserveConfigUserPassword:
			userPassword = config.Value
			if userPassword != "" && config.IsEncrypted {
				var err error
				userPassword, err = common.Decrypt(config.Value)
				if err != nil {
					return ObserveConfig{}, fmt.Errorf("failed to decrypt observe app key: %w", err)
				}
			}
		case ObserveApiToken:
			apiToken = config.Value
			if apiToken != "" && config.IsEncrypted {
				var err error
				apiToken, err = common.Decrypt(config.Value)
				if err != nil {
					return ObserveConfig{}, fmt.Errorf("failed to decrypt observe app key: %w", err)
				}
			}
		case ObserveConfigCustomerID:
			customerID = config.Value
		case ObserveConfigLogDatasetID:
			logDatasetID = config.Value
		case ObserveConfigDomain:
			domain = config.Value
		}
	}
	observeConfig := ObserveConfig{
		UserEmail:    userEmail,
		UserPassword: userPassword,
		CustomerID:   customerID,
		LogDatasetID: logDatasetID,
		ApiToken:     apiToken,
		Domain:       domain,
	}

	return observeConfig, nil
}

func (m Observe) ValidateConfig(sc *security.SecurityContext, config []core.IntegrationConfigValue, accountId string) []error {
	var errors []error
	var err error
	cfg := ObserveConfig{}
	for _, c := range config {
		switch c.Name {
		case ObserveConfigUserEmail:
			cfg.UserEmail = c.Value
		case ObserveConfigUserPassword:
			if c.Value != "" && c.IsEncrypted {
				cfg.UserPassword, err = common.Decrypt(c.Value)
				if err != nil {
					errors = append(errors, fmt.Errorf("failed to decrypt observe user password: %w", err))
				}

			} else {
				cfg.UserPassword = c.Value
			}
		case ObserveApiToken:
			if c.Value != "" && c.IsEncrypted {
				cfg.ApiToken, err = common.Decrypt(c.Value)
				if err != nil {
					errors = append(errors, fmt.Errorf("failed to decrypt observe api token: %w", err))
				}
			} else {
				cfg.ApiToken = c.Value
			}
		case ObserveConfigCustomerID:
			cfg.CustomerID = c.Value
		case ObserveConfigLogDatasetID:
			cfg.LogDatasetID = c.Value
		case ObserveConfigDomain:
			cfg.Domain = c.Value
		}
	}

	if err := m.ValidateObserveConfig(cfg); err != nil {
		errors = append(errors, fmt.Errorf("observe config validation failed: %w", err))
	}
	return errors
}

func (m Observe) GetObservDataSetInfo(accountId string, dataSetId string) (map[string]any, error) {
	observe := Observe{}
	config, err := GetObserveConfigs(nil, accountId)
	if err != nil {
		return nil, fmt.Errorf("failed to get observe config: %w", err)
	}
	token, err := observe.GetToken(config)
	if err != nil {
		return nil, fmt.Errorf("failed to get observe token: %w", err)
	}
	// curl https://OBSERVE_CUSTOMERID.observeinc.com/v1/dataset/40000068 \
	//--header 'Authorization: Bearer YOUR_SECRET_TOKEN'

	datasetURL := fmt.Sprintf("%s/v1/dataset/%s", observe.BaseURL(config), dataSetId)
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Accept":        "application/json",
		"Authorization": fmt.Sprintf("Bearer %s %s", config.CustomerID, token),
	}
	res, err := common.HttpGet(datasetURL, common.HttpWithHeaders(headers))
	if err != nil {
		return nil, fmt.Errorf("failed to create dataset info request: %w", err)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dataset info request failed: status=%d body=%s", res.StatusCode, string(body))
	}
	var datasetInfo map[string]any
	if err := json.Unmarshal(body, &datasetInfo); err != nil {
		return nil, fmt.Errorf("failed to parse dataset info response: %w", err)
	}
	return datasetInfo, nil
}
