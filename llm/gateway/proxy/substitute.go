package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/providers/anthropic"
	"github.com/maximhq/bifrost/core/providers/gemini"
	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"

	"nudgebee/llm-gateway/edgeerr"
	"nudgebee/llm-gateway/metering"
)

// substitute runs a cross-provider substitution (P2): the client sent a request in
// its native provider format, a routing rule resolved a DIFFERENT target provider, so
// we parse the client-native request into the unified schema, dispatch to the target
// via the unified engine, and re-encode the response BACK to the client's native shape
// (so the client SDK is none the wiser). It owns cancel.
//
// The client's native format dictates the pivot: an OpenAI (chat-completions) client
// uses the Chat engine (the unified response marshals as OpenAI directly, as on /v1);
// an Anthropic or Gemini client uses the Responses engine + that client's native
// re-encoder.
func (h *handler) substitute(c *gin.Context, bctx *schemas.BifrostContext, cancel func(), rc *RequestContext, rm *reqMeta) {
	target := schemas.ModelProvider(rc.Decision.ResolvedProvider)
	// The target model/provider is what actually RUNS — meter it as such (cost prices
	// the executed model); the requested provider/model stay on the decision record.
	rm.provider = target
	rm.model = rc.Decision.ResolvedModel

	// Signal the swap. The client SDK ignores these headers (a transparent swap is
	// invisible to it by design), but the operator sees them in logs/proxies and the
	// same facts land in metering (routing + degraded attributes).
	c.Writer.Header().Set("x-nb-llm-substituted",
		fmt.Sprintf("%s->%s", rc.Decision.RequestedProvider, rc.Decision.ResolvedProvider))

	if rc.Provider == schemas.OpenAI {
		h.substituteChat(c, bctx, cancel, rc, rm, target)
		return
	}
	h.substituteResponses(c, bctx, cancel, rc, rm, target)
}

// substituteChat handles an OpenAI (chat-completions) client: parse → unified Chat →
// dispatch to the target → return the canonical OpenAI shape (unary + streaming).
func (h *handler) substituteChat(c *gin.Context, bctx *schemas.BifrostContext, cancel func(), rc *RequestContext, rm *reqMeta, target schemas.ModelProvider) {
	var oaiReq openai.OpenAIChatRequest
	if err := json.Unmarshal(rc.Body, &oaiReq); err != nil {
		cancel()
		edgeerr.Write(c, string(rc.Provider), http.StatusBadRequest, "invalid_request",
			"could not parse request body as an OpenAI chat completion request")
		h.meter(context.WithoutCancel(c.Request.Context()), rm, http.StatusBadRequest, nil, nil, nil)
		return
	}
	breq := oaiReq.ToBifrostChatRequest(bctx)
	breq.Provider = target
	breq.Model = rm.model
	// The Chat engine + OpenAI response shape is exactly the generic-endpoint path.
	rc.Model = rm.model
	if rc.Streaming {
		h.streamChatWith(c, bctx, cancel, rm, breq)
		return
	}
	defer cancel()
	h.unaryChatWith(c, bctx, rm, breq)
}

// substituteResponses handles an Anthropic or Gemini client: parse the native request
// into the unified Responses schema, dispatch to the target, and re-encode the
// response back to the client's native shape (unary + streaming).
func (h *handler) substituteResponses(c *gin.Context, bctx *schemas.BifrostContext, cancel func(), rc *RequestContext, rm *reqMeta, target schemas.ModelProvider) {
	breq, ok := parseToResponses(rc.Provider, rc.Body, bctx)
	if !ok {
		cancel()
		edgeerr.Write(c, string(rc.Provider), http.StatusBadRequest, "invalid_request",
			"could not parse request body for translation")
		h.meter(context.WithoutCancel(c.Request.Context()), rm, http.StatusBadRequest, nil, nil, nil)
		return
	}
	breq.Provider = target
	breq.Model = rm.model

	// Best-effort + signal: note features the target can't honor. Core's converter drops
	// them building the target request; we record what won't survive for visibility.
	if dropped := detectDegradation(breq, target); len(dropped) > 0 {
		rm.degraded = dropped
		c.Writer.Header().Set("x-nb-llm-degraded", strings.Join(dropped, ","))
	}

	if rc.Streaming {
		h.streamResponses(c, bctx, cancel, rc, rm, breq)
		return
	}
	defer cancel()
	h.unaryResponses(c, bctx, rc, rm, breq)
}

func (h *handler) unaryResponses(c *gin.Context, bctx *schemas.BifrostContext, rc *RequestContext, rm *reqMeta, breq *schemas.BifrostResponsesRequest) {
	meterCtx := context.WithoutCancel(c.Request.Context())
	resp, bErr := h.client.ResponsesRequest(bctx, breq)
	if bErr != nil {
		status := writeBifrostError(c, bErr)
		h.meter(meterCtx, rm, status, nil, nil, nil)
		return
	}
	if resp == nil {
		edgeerr.Write(c, string(rc.Provider), http.StatusBadGateway, "upstream_error",
			"empty response from the provider engine")
		h.meter(meterCtx, rm, http.StatusBadGateway, nil, nil, nil)
		return
	}
	out, err := encodeResponsesUnary(rc.Provider, bctx, resp)
	if err != nil {
		edgeerr.Write(c, string(rc.Provider), http.StatusInternalServerError, "gateway_error",
			"could not encode translated response")
		h.meter(meterCtx, rm, http.StatusInternalServerError, nil, responsesUsage(resp), nil)
		return
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(out)))
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(out)
	h.meter(meterCtx, rm, http.StatusOK, nil, responsesUsage(resp), out)
}

func (h *handler) streamResponses(c *gin.Context, bctx *schemas.BifrostContext, cancel func(), rc *RequestContext, rm *reqMeta, breq *schemas.BifrostResponsesRequest) {
	defer cancel()

	status := http.StatusBadGateway
	var usageResp *schemas.BifrostResponsesResponse
	captureBody := metering.BodyLoggingEnabled()
	var respBuf []byte
	defer func() {
		h.meter(context.WithoutCancel(c.Request.Context()), rm, status, nil, responsesUsage(usageResp), respBuf)
	}()

	stream, bErr := h.client.ResponsesStreamRequest(bctx, breq)
	if bErr != nil {
		status = writeBifrostError(c, bErr)
		return
	}

	status = http.StatusOK
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, _ := c.Writer.(http.Flusher)
	writeSSE := func(b []byte) bool {
		if len(b) == 0 {
			return true
		}
		if _, err := c.Writer.Write(b); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}

	enc := newResponsesStreamEncoder(rc.Provider)
	for chunk := range stream {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			// Surface a native error event and stop; no success sentinel (as on /v1).
			frame := enc.errorFrame(chunk.BifrostError)
			if captureBody {
				respBuf = appendCappedBody(respBuf, frame)
			}
			writeSSE(frame)
			return
		}
		rr := chunk.BifrostResponsesStreamResponse
		if rr == nil {
			continue
		}
		if rr.Response != nil && rr.Response.Usage != nil {
			usageResp = rr.Response
		}
		frame := enc.frame(bctx, rr)
		if len(frame) == 0 {
			continue
		}
		if captureBody {
			respBuf = appendCappedBody(respBuf, frame)
		}
		if !writeSSE(frame) {
			return
		}
	}
}

// parseToResponses unmarshals a client-native body into the unified Responses request.
func parseToResponses(client schemas.ModelProvider, body []byte, bctx *schemas.BifrostContext) (*schemas.BifrostResponsesRequest, bool) {
	switch client {
	case schemas.Anthropic:
		var req anthropic.AnthropicMessageRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, false
		}
		return req.ToBifrostResponsesRequest(bctx), true
	case schemas.Gemini:
		var req gemini.GeminiGenerationRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, false
		}
		return req.ToBifrostResponsesRequest(bctx), true
	default:
		return nil, false
	}
}

// encodeResponsesUnary re-encodes a unified Responses response into the client's
// native (unary) shape.
func encodeResponsesUnary(client schemas.ModelProvider, bctx *schemas.BifrostContext, resp *schemas.BifrostResponsesResponse) ([]byte, error) {
	switch client {
	case schemas.Anthropic:
		return json.Marshal(anthropic.ToAnthropicResponsesResponse(bctx, resp))
	case schemas.Gemini:
		return json.Marshal(gemini.ToGeminiResponsesResponse(resp))
	default:
		return nil, fmt.Errorf("no responses encoder for %s", client)
	}
}

// responsesStreamEncoder re-frames unified Responses stream chunks into a client's
// native SSE wire format. Held per request (Gemini needs cross-chunk state; Anthropic
// threads state via the shared BifrostContext).
type responsesStreamEncoder struct {
	client   schemas.ModelProvider
	geminiSt *gemini.BifrostToGeminiStreamState
}

func newResponsesStreamEncoder(client schemas.ModelProvider) *responsesStreamEncoder {
	e := &responsesStreamEncoder{client: client}
	if client == schemas.Gemini {
		e.geminiSt = gemini.NewBifrostToGeminiStreamState()
	}
	return e
}

// frame returns the wire bytes for one unified chunk (empty to skip, e.g. Gemini's
// buffered chunks).
func (e *responsesStreamEncoder) frame(bctx *schemas.BifrostContext, rr *schemas.BifrostResponsesStreamResponse) []byte {
	switch e.client {
	case schemas.Anthropic:
		// One unified chunk fans out to several Anthropic events (message_start,
		// content_block_delta, …). Anthropic SSE names the event: "event: <type>".
		events := anthropic.ToAnthropicResponsesStreamResponse(bctx, rr)
		var buf []byte
		for _, ev := range events {
			if ev == nil {
				continue
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			buf = append(buf, "event: "...)
			buf = append(buf, string(ev.Type)...)
			buf = append(buf, '\n')
			buf = append(buf, "data: "...)
			buf = append(buf, data...)
			buf = append(buf, '\n', '\n')
		}
		return buf
	case schemas.Gemini:
		gr := gemini.ToGeminiResponsesStreamResponse(rr, e.geminiSt)
		if gr == nil {
			return nil // buffered chunk — nothing to emit yet
		}
		data, err := json.Marshal(gr)
		if err != nil {
			return nil
		}
		return append(append([]byte("data: "), data...), '\n', '\n')
	default:
		return nil
	}
}

// errorFrame renders a mid-stream error in the client's native SSE error shape.
func (e *responsesStreamEncoder) errorFrame(bErr *schemas.BifrostError) []byte {
	switch e.client {
	case schemas.Anthropic:
		return []byte(anthropic.ToAnthropicResponsesStreamError(bErr))
	case schemas.Gemini:
		data, err := json.Marshal(gemini.ToGeminiError(bErr))
		if err != nil {
			return nil
		}
		return append(append([]byte("data: "), data...), '\n', '\n')
	default:
		return nil
	}
}

// responsesUsage adapts a unified Responses response's usage into the passthrough-usage
// wrapper the metering layer consumes.
func responsesUsage(resp *schemas.BifrostResponsesResponse) *schemas.BifrostPassthroughUsage {
	if resp == nil || resp.Usage == nil {
		return nil
	}
	u := resp.Usage
	llm := &schemas.BifrostLLMUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.TotalTokens,
	}
	if d := u.InputTokensDetails; d != nil {
		llm.PromptTokensDetails = &schemas.ChatPromptTokensDetails{
			CachedReadTokens:  d.CachedReadTokens,
			CachedWriteTokens: d.CachedWriteTokens,
		}
	}
	return &schemas.BifrostPassthroughUsage{LLMUsage: llm, ServiceTier: resp.ServiceTier, Speed: resp.Speed}
}

// detectDegradation reports features present in the request that the target provider
// can't honor, for best-effort-and-signal substitution. Core's converter drops these
// when building the target request; we surface what won't survive.
func detectDegradation(req *schemas.BifrostResponsesRequest, target schemas.ModelProvider) []string {
	if req == nil {
		return nil
	}
	var dropped []string
	// Anthropic prompt caching (cache_control) and signed/redacted thinking blocks are
	// Anthropic-only; a non-Anthropic target can't preserve them.
	if target != schemas.Anthropic {
		if hasCacheControl(req) {
			dropped = append(dropped, "prompt_caching")
		}
		if hasSignedThinking(req) {
			dropped = append(dropped, "thinking_signature")
		}
	}
	return dropped
}

func hasCacheControl(req *schemas.BifrostResponsesRequest) bool {
	for i := range req.Input {
		msg := &req.Input[i]
		if msg.CacheControl != nil {
			return true
		}
		if msg.Content != nil {
			for j := range msg.Content.ContentBlocks {
				if msg.Content.ContentBlocks[j].CacheControl != nil {
					return true
				}
			}
		}
	}
	if req.Params != nil {
		for i := range req.Params.Tools {
			if req.Params.Tools[i].CacheControl != nil {
				return true
			}
		}
	}
	return false
}

func hasSignedThinking(req *schemas.BifrostResponsesRequest) bool {
	for i := range req.Input {
		msg := &req.Input[i]
		if msg.Content == nil {
			continue
		}
		for j := range msg.Content.ContentBlocks {
			b := &msg.Content.ContentBlocks[j]
			if b.Signature != nil || b.EncryptedContent != nil {
				return true
			}
		}
	}
	return false
}
