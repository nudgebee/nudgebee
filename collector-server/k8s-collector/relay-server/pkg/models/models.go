package models

import "encoding/json"

// ActionRequestBody represents the structure of an action call.
type ActionRequestBody struct {
	AccountID    string                 `json:"account_id" validate:"required"`
	ClusterName  string                 `json:"cluster_name" validate:"required"`
	ActionName   string                 `json:"action_name" validate:"required"`
	Timestamp    int64                  `json:"timestamp"`
	ActionParams map[string]interface{} `json:"action_params,omitempty"`
	Sinks        []string               `json:"sinks,omitempty"`
	Origin       string                 `json:"origin,omitempty"`
}

// ExternalActionRequest is the envelope for non‑Grafana RPC calls.
type ExternalActionRequest struct {
	Body      ActionRequestBody `json:"body" validate:"required"`
	Signature string            `json:"signature,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
	Cache     bool              `json:"cache,omitempty"`
	NoSink    bool              `json:"no_sinks,omitempty"`
}

// HTTPRequest is used when proxying Grafana requests over RPC.
type HTTPRequest struct {
	Method        string              `json:"method,omitempty"`
	URL           string              `json:"url,omitempty"`
	Header        map[string][]string `json:"header,omitempty"`
	ContentLength int64               `json:"content_length,omitempty"`
	Body          string              `json:"body,omitempty"`
}

// UnmarshalJSON provides backward compatibility for Header field in HTTPRequest.
// Handles both old format (map[string]string) and new format (map[string][]string).
func (h *HTTPRequest) UnmarshalJSON(data []byte) error {
	type Alias HTTPRequest
	aux := &struct {
		Header json.RawMessage `json:"header,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(h),
	}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	// If Header field is empty, nothing to do
	if len(aux.Header) == 0 {
		return nil
	}

	// Try to unmarshal as new format (map[string][]string) first
	var newFormat map[string][]string
	if err := json.Unmarshal(aux.Header, &newFormat); err == nil {
		h.Header = newFormat
		return nil
	}

	// Fall back to old format (map[string]string) and convert to new format
	var oldFormat map[string]string
	if err := json.Unmarshal(aux.Header, &oldFormat); err != nil {
		return err
	}

	// Convert old format to new format
	h.Header = make(map[string][]string, len(oldFormat))
	for k, v := range oldFormat {
		h.Header[k] = []string{v}
	}

	return nil
}

// GrafanaRPCRequest wraps a raw HTTPRequest for the Grafana endpoint.
type GrafanaRPCRequest struct {
	RequestID string      `json:"request_id"`
	AccountID string      `json:"account_id"`
	HTTPReq   HTTPRequest `json:"http_request"`
}

// HTTPResponse is what your agent returns inside AgentResponse.Data.
type HTTPResponse struct {
	StatusCode    int                 `json:"status_code,omitempty"`
	Header        map[string][]string `json:"header,omitempty"`
	ContentLength int64               `json:"content_length,omitempty"`
	Body          string              `json:"body,omitempty"`
}

// UnmarshalJSON provides backward compatibility for Header field.
// Handles both old format (map[string]string) and new format (map[string][]string).
func (h *HTTPResponse) UnmarshalJSON(data []byte) error {
	// Create a temporary struct with Header as json.RawMessage to inspect it first
	type Alias HTTPResponse
	aux := &struct {
		Header json.RawMessage `json:"header,omitempty"`
		*Alias
	}{
		Alias: (*Alias)(h),
	}

	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	// If Header field is empty, nothing to do
	if len(aux.Header) == 0 {
		return nil
	}

	// Try to unmarshal as new format (map[string][]string) first
	var newFormat map[string][]string
	if err := json.Unmarshal(aux.Header, &newFormat); err == nil {
		h.Header = newFormat
		return nil
	}

	// Fall back to old format (map[string]string) and convert to new format
	var oldFormat map[string]string
	if err := json.Unmarshal(aux.Header, &oldFormat); err != nil {
		return err
	}

	// Convert old format to new format
	h.Header = make(map[string][]string, len(oldFormat))
	for k, v := range oldFormat {
		h.Header[k] = []string{v}
	}

	return nil
}

// AgentResponse is the top‑level RPC envelope coming back from the agent.
type AgentResponse struct {
	StatusCode int    `json:"status_code,omitempty"`
	Action     string `json:"action,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
	Data       string `json:"data,omitempty"`
}

// ErrorEnvelope is a convenience for returning HTTP errors in JSON.
type ErrorEnvelope struct {
	Errors []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

type InteractiveShellRequest struct {
	AccountID string `json:"account_id" validate:"required"`
	Action    string `json:"action"`
	SessionID string `json:"session_id,omitempty"`
	Command   string `json:"command,omitempty"`
	Name      string `json:"name,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type InteractiveShellResponse struct {
	SessionID string `json:"session_id"`
	Data      string `json:"data"`
	Exit      bool   `json:"exit"`
}

// DatasourceConfig represents a single datasource configuration pushed to proxy agents.
type DatasourceConfig struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`       // e.g. "postgresql", "mysql", "http", "mcp"
	ProxyType        string            `json:"proxy_type"` // e.g. "db-proxy", "http-proxy", "mcp-proxy"
	Name             string            `json:"name,omitempty"`
	Config           map[string]any    `json:"config"`                   // host, port, database, base_url, etc.
	Credentials      map[string]string `json:"credentials,omitempty"`    // username, password, bearer_token, etc.
	CredentialSource string            `json:"credential_source"`        // cloud_push, local, aws_sm, gcp_sm, azure_kv
	CredentialRef    string            `json:"credential_ref,omitempty"` // secret name/path for cloud SM sources
}

// DatasourceConfigPush is the message sent to proxy agents over WebSocket
// when datasource configurations are created, updated, or deleted.
type DatasourceConfigPush struct {
	Action      string             `json:"action"` // "datasource_config_sync"
	RequestID   string             `json:"request_id"`
	AccountID   string             `json:"account_id"`
	Datasources []DatasourceConfig `json:"datasources"`
}

// ProxyConfigPushRequest is the HTTP request body for POST /proxy/config/push.
type ProxyConfigPushRequest struct {
	AccountID   string             `json:"account_id" validate:"required"`
	Datasources []DatasourceConfig `json:"datasources" validate:"required"`
}

// TestDatasourceConfigRequest is the HTTP request body for POST /proxy/config/test.
// Sends a single datasource config to the proxy agent for connectivity validation.
type TestDatasourceConfigRequest struct {
	AccountID  string           `json:"account_id" validate:"required"`
	Datasource DatasourceConfig `json:"datasource" validate:"required"`
}

// TestDatasourceConfigResponse is returned by POST /proxy/config/test.
type TestDatasourceConfigResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}
