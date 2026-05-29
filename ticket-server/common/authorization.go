package common

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
)

// Authorization represents authorization data
type Authorization struct {
	UserID       string  `json:"user_id"`
	TenantID     string  `json:"tenant_id"`
	AccountID    string  `json:"account_id"`
	Permission   string  `json:"permission"`
	Category     string  `json:"category"`
	ResourceType *string `json:"resource_type,omitempty"`
}

// AccessResponse represents the response from the authorization server
type AccessResponse struct {
	Access []struct {
		Allowed bool `json:"allowed"`
	} `json:"access"`
}

// added a test change which does absolutely nothing

type AccessDetail struct {
	TenantID   string `json:"tenant_id" mapstructure:"tenant_id" validate:"required"`
	Permission string `json:"permission" mapstructure:"permission" validate:"required"`
	Category   string `json:"category" mapstructure:"category" validate:"required"`
	Args       struct {
		AccountID string `json:"account_id" mapstructure:"account_id"`
	} `json:"args" mapstructure:"args" validate:"required"`
}

type Payload struct {
	UserID string         `json:"user_id"`
	Access []AccessDetail `json:"access" mapstructure:"access" validate:"required"`
}

func (auth *Authorization) HasAccess() bool {
	payload := Payload{
		UserID: auth.UserID,
		Access: []AccessDetail{
			{
				TenantID:   auth.TenantID,
				Permission: auth.Permission,
				Category:   auth.Category,
				Args: struct {
					AccountID string `json:"account_id" mapstructure:"account_id"`
				}{
					AccountID: auth.AccountID,
				},
			},
		},
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal payload:", "error", slog.AnyValue(err))
		return false
	}

	url := Config.ServiceEndpoint + "/v1/authz/validate_access"

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		slog.Error("Failed to create request:", "error", slog.AnyValue(err))
		return false
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ACTION-TOKEN", Config.ServiceApiServerToken)

	client := HttpClient()
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("Failed to send request:", "error", slog.AnyValue(err))
		return false
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			slog.Error("Failed to close response body:", "error", slog.AnyValue(cerr))
		}
	}()

	var accessResponse AccessResponse
	err = json.NewDecoder(resp.Body).Decode(&accessResponse)
	if err != nil {
		slog.Error("Failed to decode response:", "error", slog.AnyValue(err))
		return false
	}

	if len(accessResponse.Access) > 0 && accessResponse.Access[0].Allowed {
		return true
	}

	return false
}
