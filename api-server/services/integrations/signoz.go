package integrations

import (
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
	"nudgebee/services/common"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"
	"strings"
	"time"
)

func init() {
	core.RegisterIntegration(Signoz{})
}

const (
	IntegrationSignoz = "signoz"

	SignozModeSelfHosted = "self_hosted"
	SignozModeCloud      = "cloud"
)

type Signoz struct {
}

func (m Signoz) ValidateConfig(ctx *security.SecurityContext, values []core.IntegrationConfigValue, accountId string) []error {
	configMap := make(map[string]string)
	for _, c := range values {
		configMap[c.Name] = c.Value
	}

	url := normalizeSignozURL(configMap["signoz_url"])
	username := strings.TrimSpace(configMap["signoz_username"])
	password := configMap["signoz_password"]
	mode := strings.TrimSpace(configMap["signoz_mode"])
	if mode == "" {
		mode = SignozModeSelfHosted
	}

	var errs []error
	if url == "" {
		errs = append(errs, fmt.Errorf("signoz_url is required"))
	} else if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		errs = append(errs, fmt.Errorf("signoz_url must start with http:// or https:// (got %q)", url))
	}
	if username == "" {
		errs = append(errs, fmt.Errorf("signoz_username is required"))
	}
	if password == "" {
		errs = append(errs, fmt.Errorf("signoz_password is required"))
	}
	if mode != SignozModeSelfHosted && mode != SignozModeCloud {
		errs = append(errs, fmt.Errorf("signoz_mode must be %q or %q", SignozModeSelfHosted, SignozModeCloud))
	}
	if len(errs) > 0 {
		return errs
	}

	switch mode {
	case SignozModeCloud:
		return validateSignozCloud(url, username, password)
	default:
		return validateSignozSelfHosted(url, username, password)
	}
}

// normalizeSignozURL trims whitespace and strips any path/query/fragment so
// users can paste the URL they have open in the browser (e.g.
// https://champion-cub.in2.signoz.cloud/settings/my-settings).
func normalizeSignozURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := neturl.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return strings.TrimRight(raw, "/")
	}
	return parsed.Scheme + "://" + parsed.Host
}

// validateSignozSelfHosted probes POST /api/v1/login (the self-hosted JWT
// endpoint used by observability/signoz_saas.go:GetJwtToken).
func validateSignozSelfHosted(url, username, password string) []error {
	resp, err := common.HttpPost(
		fmt.Sprintf("%s/api/v1/login", url),
		common.HttpWithJsonBody(map[string]string{"email": username, "password": password}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return []error{fmt.Errorf("failed to connect to SigNoz at %s: %w", url, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		var parsed struct {
			AccessJwt string `json:"accessJwt"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil || parsed.AccessJwt == "" {
			return []error{fmt.Errorf("SigNoz login returned 200 but no JWT — if this is SigNoz Cloud, switch signoz_mode to %q", SignozModeCloud)}
		}
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return []error{fmt.Errorf("invalid SigNoz credentials (HTTP %d)", resp.StatusCode)}
	case http.StatusNotFound:
		return []error{fmt.Errorf("SigNoz /api/v1/login not found at %s — if this is SigNoz Cloud, switch signoz_mode to %q", url, SignozModeCloud)}
	default:
		return []error{fmt.Errorf("SigNoz returned unexpected status: HTTP %d", resp.StatusCode)}
	}
}

// validateSignozCloud performs the two-step Cloud handshake:
//
//	GET  /api/v2/sessions/context?email=<email>   → resolve orgID
//	POST /api/v2/sessions/email_password          → JWT (accessToken)
func validateSignozCloud(url, username, password string) []error {
	ctxResp, err := common.HttpGet(
		fmt.Sprintf("%s/api/v2/sessions/context?email=%s", url, neturl.QueryEscape(username)),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return []error{fmt.Errorf("failed to connect to SigNoz Cloud at %s: %w", url, err)}
	}
	defer func() { _ = ctxResp.Body.Close() }()

	if ctxResp.StatusCode != http.StatusOK {
		return []error{fmt.Errorf("SigNoz Cloud sessions/context returned HTTP %d — check signoz_url", ctxResp.StatusCode)}
	}

	var ctxParsed struct {
		Data struct {
			Exists bool `json:"exists"`
			Orgs   []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"orgs"`
		} `json:"data"`
	}
	if err := json.NewDecoder(ctxResp.Body).Decode(&ctxParsed); err != nil {
		return []error{fmt.Errorf("SigNoz Cloud sessions/context returned unparseable response (is signoz_url a SigNoz Cloud tenant URL?): %w", err)}
	}
	if !ctxParsed.Data.Exists || len(ctxParsed.Data.Orgs) == 0 {
		return []error{fmt.Errorf("SigNoz Cloud reports no organization for email %s on %s", username, url)}
	}
	orgID := ctxParsed.Data.Orgs[0].ID

	loginResp, err := common.HttpPost(
		fmt.Sprintf("%s/api/v2/sessions/email_password", url),
		common.HttpWithJsonBody(map[string]string{
			"email":    username,
			"password": password,
			"orgID":    orgID,
		}),
		common.HttpWithTimeout(15*time.Second),
	)
	if err != nil {
		return []error{fmt.Errorf("failed to call SigNoz Cloud login: %w", err)}
	}
	defer func() { _ = loginResp.Body.Close() }()

	switch loginResp.StatusCode {
	case http.StatusOK:
		var parsed struct {
			Data struct {
				AccessToken string `json:"accessToken"`
			} `json:"data"`
		}
		if err := json.NewDecoder(loginResp.Body).Decode(&parsed); err != nil || parsed.Data.AccessToken == "" {
			return []error{fmt.Errorf("SigNoz Cloud login returned 200 but no accessToken")}
		}
		return nil
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusBadRequest:
		return []error{fmt.Errorf("invalid SigNoz Cloud credentials (HTTP %d)", loginResp.StatusCode)}
	default:
		return []error{fmt.Errorf("SigNoz Cloud login returned unexpected status: HTTP %d", loginResp.StatusCode)}
	}
}

func (m Signoz) Name() string {
	return IntegrationSignoz
}

func (m Signoz) Category() core.IntegrationCategory {
	return core.IntegrationCategoryLog
}

func (m Signoz) ConfigSchema() core.IntegrationSchema {
	return core.IntegrationSchema{
		Type:     core.ToolSchemaTypeObject,
		Required: []string{"signoz_url", "signoz_username", "signoz_password", "signoz_mode"},
		Testable: true,
		Properties: map[string]core.IntegrationSchemaProperty{
			"signoz_mode": {
				Type:        core.ToolSchemaTypeString,
				Description: "SigNoz deployment type. Pick 'cloud' for *.signoz.cloud tenants; 'self_hosted' for your own SigNoz instance.",
				Default:     SignozModeSelfHosted,
				Enum:        []any{SignozModeSelfHosted, SignozModeCloud},
				Priority:    90,
				IsTestable:  true,
			},
			"signoz_url": {
				Type:        core.ToolSchemaTypeString,
				Description: "Base URL of the SigNoz instance (e.g., https://signoz.example.com or https://<tenant>.signoz.cloud).",
				Priority:    85,
				IsTestable:  true,
			},
			"signoz_username": {
				Type:        core.ToolSchemaTypeString,
				Description: "Email/username for authenticating with SigNoz.",
				Priority:    72,
				IsTestable:  true,
			},
			"signoz_password": {
				Type:        core.ToolSchemaTypeString,
				Description: "Password for authenticating with SigNoz.",
				IsEncrypted: true,
				Priority:    70,
				IsTestable:  true,
			},
			core.IntegrationConfigName: {
				Type:             core.ToolSchemaTypeString,
				Description:      "Custom name for this SigNoz integration.",
				Default:          "",
				AutoGenerateFunc: "",
				Priority:         100,
			},
			core.AccountId: {
				Type:             core.ToolSchemaTypeArray,
				Description:      "Associated account(s) for this integration.",
				Default:          "",
				AutoGenerateFunc: "listAccounts",
				Priority:         95,
			},
			core.DefaultLogProvider: {
				Type:             core.ToolSchemaTypeBoolean,
				Description:      "Make SigNoz default Log Provider",
				Default:          false,
				AutoGenerateFunc: "",
				Priority:         15,
			},
		},
	}
}
