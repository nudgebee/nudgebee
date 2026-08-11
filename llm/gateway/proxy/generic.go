package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/edgeerr"
	"nudgebee/llm-gateway/routing"
)

// chatCompletionsPath is the canonical path recorded for generic-endpoint requests.
const chatCompletionsPath = "/v1/chat/completions"

// handleChat serves the gateway's provider-agnostic, OpenAI-compatible endpoint
// (POST /v1/chat/completions). Unlike the native mounts, the provider is derived
// from the model name (explicit "provider/model" or a well-known prefix); the
// request is parsed into Bifrost's unified chat schema and dispatched through the
// unified engine (so it can target any provider); and the response is returned in
// the canonical OpenAI shape. The same auth + control pipeline (route → ratelimit →
// resolver → filter) and metering as the native mounts apply.
func (h *handler) handleChat(c *gin.Context) {
	start := time.Now()

	body, err := readBody(c)
	if err != nil {
		id := auth.FromContext(c)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			edgeerr.Write(c, edgeerr.OpenAI, http.StatusRequestEntityTooLarge, "request_too_large",
				"request body exceeds the gateway limit")
			h.recordReject(id, schemas.OpenAI, "", c.Request.Method, chatCompletionsPath, http.StatusRequestEntityTooLarge, "too_large", start)
			return
		}
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusBadRequest, "invalid_request", "could not read request body")
		h.recordReject(id, schemas.OpenAI, "", c.Request.Method, chatCompletionsPath, http.StatusBadRequest, "bad_request", start)
		return
	}

	logRequestProvenance(c, body)

	identity := auth.FromContext(c)

	requestedModel, streaming := parseBody(body)
	// Resolution order — a tenant's EXPLICITLY-configured upstream wins over the built-in
	// heuristics. A custom / vertex_openai model (matched by the exact configured id) is
	// checked FIRST: otherwise a configured MaaS id like "google/gemma-…-maas" would be
	// hijacked by the "google" → Gemini provider alias and sent to generateContent instead of
	// the tenant's endpoint. Falling through: a known "provider/model" or bare name, then a
	// tier alias (e.g. "nb-fast") a routing rule maps to a provider, else a clear 400.
	// customKey, when set, is that upstream's per-request credential (carries its base URL),
	// injected as a DirectKey so the request routes on the vLLM lane.
	var provider schemas.ModelProvider
	var model string
	var customKey *schemas.Key
	var customKeyPath string
	if cp, key, path, isCustom := resolveCustomProvider(identity.TenantID, requestedModel); isCustom {
		provider, model = cp, requestedModel
		customKey = &key
		customKeyPath = path
	} else if p, m, known := resolveModelProvider(requestedModel); known {
		provider, model = p, m
	} else if lane, isTier := h.tierLane(identity, requestedModel); isTier {
		provider, model = lane, requestedModel
	} else {
		msg := `unknown or missing model; address it as "provider/model" (e.g. "anthropic/claude-opus-4-8") or a known model name`
		if config.Config.TiersEnabled {
			msg += `, or a tier alias (nb-fast/nb-cheap/nb-smart)`
		}
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusBadRequest, "invalid_request", msg)
		h.recordReject(identity, schemas.OpenAI, requestedModel, c.Request.Method, chatCompletionsPath, http.StatusBadRequest, "unknown_model", start)
		return
	}
	bctx, cancel := schemas.NewBifrostContextWithCancel(c.Request.Context())

	// The routed provider/model feed the control pipeline (rules may re-map the model
	// or block); the canonical path is recorded for metering.
	rc := &RequestContext{
		Gin: c, Ctx: c.Request.Context(), Bctx: bctx,
		Identity: identity, Provider: provider,
		Model: model, Path: chatCompletionsPath, Body: body, Streaming: streaming,
		DirectKey: customKey, DirectKeyURLPath: customKeyPath,
	}
	if stop, err := h.pipeline.Run(rc); err != nil {
		cancel()
		slog.Error("proxy: chat pipeline error", "error", err, "provider", provider, "model", model)
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusInternalServerError, "gateway_error", "request pipeline error")
		h.recordReject(identity, provider, model, c.Request.Method, chatCompletionsPath, http.StatusInternalServerError, "gateway_error", start)
		return
	} else if stop {
		cancel() // a stage already wrote the rejection (429, no-creds 403, or a block 403)
		h.recordRejectPipeline(rc, c.Writer.Status(), start)
		return
	}

	// Honor a cross-provider substitution rule on /v1 too, so org routing policy applies
	// regardless of which endpoint a client uses. The response is canonical OpenAI
	// regardless of the target, so no re-encoding is needed — just dispatch to the
	// resolved provider/model (metering then attributes the row to the target).
	if rc.Decision.Reason == routing.ReasonSubstitute {
		c.Writer.Header().Set("x-nb-llm-substituted",
			fmt.Sprintf("%s->%s", rc.Decision.RequestedProvider, rc.Decision.ResolvedProvider))
		rc.Provider = schemas.ModelProvider(rc.Decision.ResolvedProvider)
		rc.Model = rc.Decision.ResolvedModel
	}
	// Deprecation shield: the retired model was rewritten to its replacement (route
	// stage) — signal it. Metering carries reason=deprecated via the decision.
	if rc.Decision.Reason == routing.ReasonDeprecated {
		c.Writer.Header().Set("x-nb-llm-deprecated",
			fmt.Sprintf("%s->%s", rc.Decision.RequestedModel, rc.Decision.ResolvedModel))
	}

	fingerprint := prefixFingerprint(rc.Identity, rc.Body)
	sessionID, sessionSource := resolveSession(c, rc.Body, fingerprint)
	rm := &reqMeta{
		reqID:         uuid.NewString(),
		provider:      rc.Provider,
		model:         rc.Model,
		method:        c.Request.Method,
		path:          chatCompletionsPath,
		body:          rc.Body,
		streaming:     streaming,
		surface:       surfaceGeneric,
		dlp:           rc.DLP,
		start:         start,
		sessionID:     sessionID,
		sessionSource: sessionSource,
		prefixFinger:  fingerprint,
		reqAttrs:      extractRequestAttributes(rc.Provider, rc.Body),
		identity:      identity,
		decision:      rc.Decision,
	}
	if streaming {
		h.streamChat(c, bctx, cancel, rc, rm)
		return
	}
	defer cancel()
	h.unaryChat(c, bctx, rc, rm)
}

// tierLane resolves a tier-alias model name (e.g. "nb-fast") to the provider its
// routing rule targets, so the generic /v1 endpoint — which has no addressed provider
// — can pick a lane. Returns ok=false when the name isn't a tier the rules map to a
// concrete target. The caller keeps the tier TOKEN as the model; the route stage then
// does the authoritative token→model resolution and records reason=alias.
func (h *handler) tierLane(id auth.Identity, model string) (schemas.ModelProvider, bool) {
	// Tiering disabled deployment-wide: don't resolve tier aliases, even if a stale
	// tenant override rule for one still exists — nb-* falls through to a 400.
	if !config.Config.TiersEnabled || h.router == nil {
		return "", false
	}
	d := h.router.Resolve(routing.Input{Model: model, TenantID: id.TenantID, UserID: id.UserID})
	if d.Denied || d.ResolvedProvider == "" || d.ResolvedModel == d.RequestedModel {
		return "", false // not a tier the rules resolve to a concrete provider/model
	}
	return schemas.ModelProvider(d.ResolvedProvider), true
}

// unaryChat parses the OpenAI body into the unified chat request, pins the routed
// provider/model, and dispatches through the shared chat core. Metering runs on
// every exit path.
func (h *handler) unaryChat(c *gin.Context, bctx *schemas.BifrostContext, rc *RequestContext, rm *reqMeta) {
	var oaiReq openai.OpenAIChatRequest
	if err := json.Unmarshal(rc.Body, &oaiReq); err != nil {
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusBadRequest, "invalid_request",
			"could not parse request body as an OpenAI chat completion request")
		h.meter(context.WithoutCancel(c.Request.Context()), rm, http.StatusBadRequest, nil, nil, nil)
		return
	}
	// Preserve provider-specific params the unified schema doesn't model (e.g. vLLM's
	// chat_template_kwargs) so passthrough-capable lanes forward them to the upstream.
	applyChatExtraParams(&oaiReq, rc.Body)
	sanitizeChatMetadata(&oaiReq) // OpenAI `metadata` must be string→string; strict providers (Vertex) 400 otherwise
	breq := oaiReq.ToBifrostChatRequest(bctx)
	// Pin the routed target: the client addressed a model name; routing may have
	// re-mapped it, and the provider is derived from that name, not the request body.
	breq.Provider = rc.Provider
	breq.Model = rc.Model
	h.unaryChatWith(c, bctx, rm, breq)
}

// unaryChatWith dispatches a prebuilt chat request through the unified engine and
// returns the canonical OpenAI response. Shared by the generic endpoint and
// OpenAI-client cross-provider substitution.
func (h *handler) unaryChatWith(c *gin.Context, bctx *schemas.BifrostContext, rm *reqMeta, breq *schemas.BifrostChatRequest) {
	meterCtx := context.WithoutCancel(c.Request.Context())

	resp, bErr := h.client.ChatCompletionRequest(bctx, breq)
	if bErr != nil {
		status := writeBifrostError(c, bErr)
		h.meter(meterCtx, rm, status, nil, nil, nil)
		return
	}
	if resp == nil {
		// Defensive: no error but no response — never return a "200 null" to the client.
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusBadGateway, "upstream_error",
			"empty response from the provider engine")
		h.meter(meterCtx, rm, http.StatusBadGateway, nil, nil, nil)
		return
	}

	rm.respondedModel = resp.Model // provider-echoed model (from the parsed response)
	out, err := marshalOpenAIChat(resp)
	if err != nil {
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusInternalServerError, "gateway_error", "could not encode response")
		h.meter(meterCtx, rm, http.StatusInternalServerError, nil, chatUsage(resp), nil)
		return
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(out)))
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(out)

	h.meter(meterCtx, rm, http.StatusOK, nil, chatUsage(resp), out)
}

// genericModelCatalog is an advisory list of current-generation models for the
// generic endpoint, in "provider/model" form. It powers model pickers in
// OpenAI-compatible tools; it is NOT a whitelist — the endpoint accepts any valid
// "provider/model" (or well-known bare name), listed here or not.
var genericModelCatalog = []struct{ id, ownedBy string }{
	// NB tier aliases — provider-agnostic names that resolve to a concrete model
	// (see routing.DefaultTierRules); listed first so pickers surface them up top.
	{"nb-fast", "nudgebee"},
	{"nb-cheap", "nudgebee"},
	{"nb-smart", "nudgebee"},
	{"anthropic/claude-opus-4-8", "anthropic"},
	{"anthropic/claude-sonnet-4-6", "anthropic"},
	{"anthropic/claude-haiku-4-5", "anthropic"},
	{"openai/gpt-5", "openai"},
	{"openai/gpt-5-mini", "openai"},
	{"openai/o4-mini", "openai"},
	{"gemini/gemini-3.1-pro", "gemini"},
	{"gemini/gemini-3.1-flash", "gemini"},
	{"gemini/gemini-3.1-flash-lite", "gemini"},
	{"huggingface/meta-llama/Llama-3.1-8B-Instruct", "huggingface"},
	{"huggingface/deepseek-ai/DeepSeek-V3", "huggingface"},
	{"bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0", "bedrock"},
	{"bedrock/anthropic.claude-3-5-haiku-20241022-v1:0", "bedrock"},
}

// handleModels serves GET /v1/models in OpenAI's list shape so model pickers in
// OpenAI-compatible tools populate. It is a static metadata call (not metered).
func (h *handler) handleModels(c *gin.Context) {
	type model struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		OwnedBy string `json:"owned_by"`
	}
	data := make([]model, 0, len(genericModelCatalog))
	for _, m := range genericModelCatalog {
		// Don't advertise the tier aliases when tiering is disabled deployment-wide
		// (the tier rows are the "nudgebee"-owned entries).
		if m.ownedBy == "nudgebee" && !config.Config.TiersEnabled {
			continue
		}
		data = append(data, model{ID: m.id, Object: "model", OwnedBy: m.ownedBy})
	}
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": data})
}

// streamChat dispatches through the unified streaming engine and re-frames each
// unified chat chunk as an OpenAI `chat.completion.chunk` SSE event, ending with
// the `data: [DONE]` sentinel. Usage arrives on the final chunk (include_usage is
// on by default) and is metered once on every exit path.
func (h *handler) streamChat(c *gin.Context, bctx *schemas.BifrostContext, cancel func(), rc *RequestContext, rm *reqMeta) {
	var oaiReq openai.OpenAIChatRequest
	if err := json.Unmarshal(rc.Body, &oaiReq); err != nil {
		cancel()
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusBadRequest, "invalid_request",
			"could not parse request body as an OpenAI chat completion request")
		h.meter(context.WithoutCancel(c.Request.Context()), rm, http.StatusBadRequest, nil, nil, nil)
		return
	}
	applyChatExtraParams(&oaiReq, rc.Body)
	sanitizeChatMetadata(&oaiReq) // OpenAI `metadata` must be string→string; strict providers (Vertex) 400 otherwise
	breq := oaiReq.ToBifrostChatRequest(bctx)
	breq.Provider = rc.Provider
	breq.Model = rc.Model
	h.streamChatWith(c, bctx, cancel, rm, breq)
}

// streamChatWith streams a prebuilt chat request as OpenAI `chat.completion.chunk`
// SSE, ending with `data: [DONE]`. Usage arrives on the final chunk and is metered
// once on every exit path. Shared by the generic endpoint and OpenAI-client
// substitution. Owns cancel.
func (h *handler) streamChatWith(c *gin.Context, bctx *schemas.BifrostContext, cancel func(), rm *reqMeta, breq *schemas.BifrostChatRequest) {
	defer cancel()

	status := http.StatusBadGateway
	var usageResp *schemas.BifrostChatResponse
	captureBody := bodyCaptureAllowed(rm.identity.TenantID)
	var respBuf []byte
	defer func() {
		h.meter(context.WithoutCancel(c.Request.Context()), rm, status, nil, chatUsage(usageResp), respBuf)
	}()

	stream, bErr := h.client.ChatCompletionStreamRequest(bctx, breq)
	if bErr != nil {
		status = writeBifrostError(c, bErr)
		return
	}

	// From here the client has received a 200 SSE stream.
	status = http.StatusOK
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, _ := c.Writer.(http.Flusher)
	writeSSE := func(b []byte) bool {
		if _, err := c.Writer.Write(b); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	for chunk := range stream {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			// Mid-stream error: the 200 header is already sent, so we can't change the
			// status. Surface the error as an OpenAI-shaped SSE error event and stop
			// WITHOUT the [DONE] sentinel — [DONE] signals a complete, successful stream
			// and would hide that this one was cut short.
			frame := sseFrame(gin.H{"error": openAIStreamError(chunk.BifrostError)})
			if captureBody {
				respBuf = appendCappedBody(respBuf, frame)
			}
			writeSSE(frame)
			return
		}
		cr := chunk.BifrostChatResponse
		if cr == nil {
			continue
		}
		if cr.Usage != nil {
			usageResp = cr
		}
		if cr.Model != "" {
			rm.respondedModel = cr.Model
		}
		data, err := marshalOpenAIChat(cr)
		if err != nil {
			continue
		}
		frame := append(append([]byte("data: "), data...), '\n', '\n')
		if captureBody {
			respBuf = appendCappedBody(respBuf, frame)
		}
		if !writeSSE(frame) {
			return // client gone; deferred meter records what we have
		}
	}
	done := []byte("data: [DONE]\n\n")
	if captureBody {
		respBuf = appendCappedBody(respBuf, done)
	}
	writeSSE(done)
}

// sseFrame encodes v as a single SSE data event ("data: <json>\n\n").
func sseFrame(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return append(append([]byte("data: "), data...), '\n', '\n')
}

// openAIStreamError shapes a mid-stream provider error as an OpenAI error object.
func openAIStreamError(bErr *schemas.BifrostError) gin.H {
	msg := "provider engine error"
	if bErr != nil && bErr.Error != nil && bErr.Error.Message != "" {
		msg = bErr.Error.Message
	}
	return gin.H{"message": coldStartHint(statusOf(bErr), msg), "type": "api_error"}
}

// openAIChatView marshals a unified chat response in clean OpenAI shape, dropping
// Bifrost's internal `extra_fields` annotation. That block carries the operator's
// provider org/project ids and raw provider response headers (rate limits, request
// ids) — internal routing detail that must not leak to the client, and which a real
// OpenAI response never contains. The embedded field is shadowed by the same JSON
// name at a shallower depth, and omitempty on a nil value drops it entirely.
type openAIChatView struct {
	*schemas.BifrostChatResponse
	ExtraFields any `json:"extra_fields,omitempty"`
}

// marshalOpenAIChat renders a unified chat response/chunk as clean OpenAI JSON.
func marshalOpenAIChat(resp *schemas.BifrostChatResponse) ([]byte, error) {
	return json.Marshal(openAIChatView{BifrostChatResponse: resp})
}

// chatUsage adapts the unified chat response's usage into the passthrough-usage
// wrapper the metering layer consumes, so the generic path reuses NewEvent verbatim.
func chatUsage(resp *schemas.BifrostChatResponse) *schemas.BifrostPassthroughUsage {
	if resp == nil {
		return nil
	}
	return &schemas.BifrostPassthroughUsage{
		LLMUsage:    resp.Usage,
		ServiceTier: resp.ServiceTier,
		Speed:       resp.Speed,
	}
}
