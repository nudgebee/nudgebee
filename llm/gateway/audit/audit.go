// Package audit is a thin HTTP client to the central audit ingest on services-server
// (api-server): POST {service_api_server_url}/v1/audit, authenticated with the shared
// X-ACTION-TOKEN. It mirrors llm-server's audit client (nudgebee/llm/audit) — the
// gateway is a separate module, so it can't import that package, but it records into
// the SAME central audit table via the same endpoint (no parallel audit store).
//
// Emission is best-effort: a failure is logged, never propagated to the request that
// triggered it (an admin config change still succeeds even if audit ingest is down).
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"nudgebee/llm-gateway/config"
)

type (
	EventCategory string
	EventType     string
	EventAction   string
	EventStatus   string
)

const (
	// CategoryAIGateway groups all AI-Gateway admin/config audits.
	CategoryAIGateway EventCategory = "AI_GATEWAY"

	// EventDataPrivacyUpdate is a change to a tenant's data-governance settings
	// (body capture / DLP mode) — a data-custody-sensitive, auditable act.
	EventDataPrivacyUpdate EventType = "GATEWAY_DATA_PRIVACY_UPDATE"

	ActionUpdate EventAction = "UPDATE"

	StatusSuccess EventStatus = "SUCCESS"
	StatusFailure EventStatus = "FAILURE"

	// ActorGatewayService labels an audit whose actor user is unknown (empty).
	ActorGatewayService = "LLM_GATEWAY_SERVICE"
)

// Audit is one audit event. JSON tags match the services-server /v1/audit contract
// (nudgebee/services/audit.Audit); the endpoint persists each with its own actor.
type Audit struct {
	UserId         string         `json:"user_id,omitempty"` // uuid of the acting user (validated uuid4 on ingest)
	TenantId       string         `json:"tenant_id,omitempty"`
	AccountId      string         `json:"account_id,omitempty"`
	EventTime      time.Time      `json:"event_time"`
	EventCategory  EventCategory  `json:"event_category"`
	EventType      EventType      `json:"event_type"`
	EventPrevState any            `json:"event_prev_state,omitempty"`
	EventState     any            `json:"event_state"`
	EventActor     string         `json:"event_actor"`
	EventTarget    string         `json:"event_target"`
	EventAction    EventAction    `json:"event_action"`
	EventStatus    EventStatus    `json:"event_status"`
	EventAttr      map[string]any `json:"event_attr,omitempty"`
}

// AuditRequest is the /v1/audit body (a batch of audits).
type AuditRequest struct {
	Audits []Audit `json:"audits"`
}

var httpClient = &http.Client{Timeout: 10 * time.Second}

// Send posts the audits to services-server. Best-effort: returns an error for the
// caller to log, but callers should not fail their operation on it. A no-op (nil)
// when the endpoint or token is unconfigured — audit is optional infrastructure.
func Send(ctx context.Context, logger *slog.Logger, req *AuditRequest) error {
	url := config.Config.ServiceApiServerURL
	token := config.Config.ActionApiServerToken
	if url == "" || token == "" {
		logger.Debug("audit: service_api_server_url/action_api_server_token unset — skipping audit emission")
		return nil
	}
	if req == nil || len(req.Audits) == 0 {
		return nil
	}
	if url[len(url)-1] != '/' {
		url += "/"
	}
	url += "v1/audit"

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("audit: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("audit: new request: %w", err)
	}
	httpReq.Header.Set("X-ACTION-TOKEN", token)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("audit: send: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("audit: ingest returned %d", resp.StatusCode)
	}
	return nil
}
