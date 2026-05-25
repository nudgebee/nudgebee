package playbooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"nudgebee/services/common"
	"nudgebee/services/config"
)

// nubiEnricherAction posts the configured query to the llm-server's
// /v1/completions/chat endpoint and attaches the LLM response as a
// markdown evidence block to the Finding.
//
// Emitted evidence shape (consumed by llm-server tool_event.go:172 and the
// UI's Ask-Nubi card):
//
//	{
//	  "type":           "markdown",
//	  "data":           "<query text>",
//	  "additional_info": {"type": "nubi_enricher", "title": "Ask Nubi", ...},
//	  "llm_response":   {"session_id": "...", "response": [...], ...}
//	}
//
// No CanAutoExecute — each invocation costs an LLM call, so the action
// must be opted into per playbook chain via agent_playbook config.
type nubiEnricherAction struct{}

type nubiEnricherParams struct {
	// Prompt is the LLM input. Matches event_actions_template.json's
	// `prompt` field. `query` is an alias for backwards compat with
	// the legacy collector path.
	Prompt string `json:"prompt,omitempty"`
	Query  string `json:"query,omitempty"`
	Title  string `json:"title,omitempty"`
	Source string `json:"source,omitempty"`
	// AsyncOverride lets a playbook author force a synchronous call when
	// the chain needs the answer inline. Defaults to async (matches the
	// Python collector's flow which always polled).
	AsyncOverride *bool `json:"async,omitempty"`
}

func (a *nubiEnricherAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	var params nubiEnricherParams
	if err := common.UnmarshalMapToStruct(rawParams, &params); err != nil {
		return nil, fmt.Errorf("nubi_enricher: parse params: %w", err)
	}
	prompt := params.Prompt
	if prompt == "" {
		prompt = params.Query
	}
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("nubi_enricher: prompt is required")
	}
	if config.Config.LLMServerEndpoint == "" {
		return nil, errors.New("nubi_enricher: llm_server_endpoint not configured")
	}
	if params.Source == "" {
		params.Source = "Investigation"
	}
	async := true
	if params.AsyncOverride != nil {
		async = *params.AsyncOverride
	}

	tenantID := ctx.GetTenantId()
	accountID := ctx.GetAccountId()
	sessionID := ctx.GetEvent().EventId
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	url := strings.TrimRight(config.Config.LLMServerEndpoint, "/") + "/v1/completions/chat"
	payload := map[string]any{
		"query":      prompt,
		"account_id": accountID,
		"tenant_id":  tenantID,
		"user_id":    "00000000-0000-0000-0000-000000000000",
		"async":      async,
		"source":     params.Source,
		"session_id": sessionID,
	}
	headers := map[string]string{
		"Content-Type": "application/json",
		"x-tenant-id":  tenantID,
	}
	if config.Config.LLMServerTokenHeader != "" && config.Config.LLMServerToken != "" {
		headers[config.Config.LLMServerTokenHeader] = config.Config.LLMServerToken
	}

	resp, err := common.HttpPost(url,
		common.HttpWithJsonBody(payload),
		common.HttpWithHeaders(headers),
		common.HttpWithTimeout(300*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nubi_enricher: post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("nubi_enricher: HTTP %d: %s", resp.StatusCode, string(body))
	}

	llmResp := parseNubiLLMResponse(body)

	title := params.Title
	if title == "" {
		title = "Ask Nubi"
	}
	additionalInfo := map[string]any{
		// `type` is the consumer-facing discriminator (llm-server's
		// tool_event keys on additional_info.type == "nubi_enricher").
		// `action_name` / `actual_action_name` mirror the legacy
		// collector path so downstream filters work unchanged.
		"type":               "nubi_enricher",
		"title":              title,
		"action_name":        "nubi_enricher",
		"actual_action_name": "nubi_enricher",
	}

	return nubiEnricherResponse{
		Data:           prompt,
		AdditionalInfo: additionalInfo,
		Insight:        []PlaybookActionResponseInsight{},
		LLMResponse:    llmResp,
	}, nil
}

// parseNubiLLMResponse accepts both the Hasura wrap {"data":{...}} and a
// flat {"session_id":...} shape. Returns an empty map (never nil) when
// the body is unparseable so the evidence still has the field set.
func parseNubiLLMResponse(body []byte) map[string]any {
	var wrapped struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && wrapped.Data != nil {
		return wrapped.Data
	}
	var flat map[string]any
	if err := json.Unmarshal(body, &flat); err == nil && flat != nil {
		return flat
	}
	return map[string]any{}
}

// nubiEnricherResponse adds the top-level `llm_response` field to the
// standard markdown evidence shape — the consumer in llm-server's
// tool_event.go:172 reads `evidence.LLMResponse.session_id` directly,
// not from the nested `data` blob.
type nubiEnricherResponse struct {
	Data           string                          `json:"data"`
	AdditionalInfo map[string]any                  `json:"additional_info"`
	Insight        []PlaybookActionResponseInsight `json:"insight"`
	LLMResponse    map[string]any                  `json:"llm_response,omitempty"`
}

func (r nubiEnricherResponse) GetFormatName() string                        { return "markdown" }
func (r nubiEnricherResponse) GetData() any                                 { return r.Data }
func (r nubiEnricherResponse) GetAdditionalInfo() map[string]any            { return r.AdditionalInfo }
func (r nubiEnricherResponse) GetInsights() []PlaybookActionResponseInsight { return r.Insight }
