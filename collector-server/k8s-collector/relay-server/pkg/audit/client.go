// Package audit emits audit events to api-server's /v1/audit ingest endpoint.
//
// relay-server is a separate Go module and cannot import api-server's audit
// package, so the wire shape (Event) and the category/type/actor literals are
// mirrored here and MUST stay byte-identical to
// api-server/services/audit/model.go.
package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	auditPath = "/v1/audit"

	// CategoryK8sRelay mirrors audit.EventCategoryK8sRelay.
	CategoryK8sRelay = "K8S_RELAY"

	// TypeK8sRelayAgentConnected / Disconnected mirror the audit.EventType
	// constants of the same name.
	TypeK8sRelayAgentConnected    = "K8SRELAY_AGENT_CONNECTED"
	TypeK8sRelayAgentDisconnected = "K8SRELAY_AGENT_DISCONNECTED"

	// ActorK8sCollectorService mirrors audit.EventActorK8sCollectorService.
	ActorK8sCollectorService = "K8S_COLLECTOR_SERVICE"

	// ActionUpdate mirrors audit.EventActionUpdate.
	ActionUpdate = "UPDATE"

	// StatusSuccess mirrors audit.EventStatusSuccess.
	StatusSuccess = "SUCCESS"
)

// Event is the subset of api-server's audit.Audit that relay-server populates.
// JSON tags match api-server/services/audit/model.go so the ingest endpoint
// unmarshals it directly.
type Event struct {
	UserId        string         `json:"user_id,omitempty"`
	TenantId      string         `json:"tenant_id,omitempty"`
	AccountId     string         `json:"account_id,omitempty"`
	EventTime     time.Time      `json:"event_time"`
	EventCategory string         `json:"event_category"`
	EventType     string         `json:"event_type"`
	EventState    any            `json:"event_state"`
	EventActor    string         `json:"event_actor"`
	EventTarget   string         `json:"event_target"`
	EventAction   string         `json:"event_action"`
	EventStatus   string         `json:"event_status"`
	EventAttr     map[string]any `json:"event_attr,omitempty"`
}

type request struct {
	Audits []Event `json:"audits"`
}

// Client posts audit events to api-server's /v1/audit ingest endpoint.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// NewClient returns a Client, or nil if baseURL or token is unset. A nil Client
// is a valid no-op receiver, so relay-server still runs without audit wiring.
func NewClient(baseURL, token string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	token = strings.TrimSpace(token)
	if baseURL == "" || token == "" {
		return nil
	}
	return &Client{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// Emit best-effort posts a single audit event. It never returns an error;
// failures are logged. Safe to call on a nil Client (no-op) and safe to run in
// a goroutine — it uses its own bounded context, independent of any request
// lifecycle, so a websocket teardown can't cancel an in-flight audit.
func (c *Client) Emit(logger *slog.Logger, ev Event) {
	if c == nil {
		return
	}
	if ev.TenantId == "" {
		// api-server drops audits with no tenant; skip rather than round-trip.
		logger.Warn("audit: skipping relay audit with empty tenant", "event_type", ev.EventType)
		return
	}

	body, err := json.Marshal(request{Audits: []Event{ev}})
	if err != nil {
		logger.Error("audit: marshal failed", "err", err, "event_type", ev.EventType)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+auditPath, bytes.NewReader(body))
	if err != nil {
		logger.Error("audit: build request failed", "err", err, "event_type", ev.EventType)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ACTION-TOKEN", c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		logger.Error("audit: post failed", "err", err, "event_type", ev.EventType)
		return
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		logger.Error("audit: ingest returned non-200", "status", resp.StatusCode, "event_type", ev.EventType)
	}
}
