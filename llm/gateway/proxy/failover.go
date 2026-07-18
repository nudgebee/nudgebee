package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"

	"nudgebee/llm-gateway/routing"
)

// isRetryable reports whether a primary failure warrants a fallback attempt — a
// transient provider error a different model/provider can plausibly fix. Client
// errors (4xx other than 429) are the caller's fault and return immediately.
func isRetryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// failoverTarget resolves a fallback endpoint to a concrete (provider, model). An
// empty fallback provider means the addressed provider; an empty model is unusable
// for failover (retrying the same model won't help) and is skipped by the caller.
func failoverTarget(requested string, fb routing.Endpoint) (schemas.ModelProvider, string) {
	provider := fb.Provider
	if provider == "" {
		provider = requested
	}
	return schemas.ModelProvider(provider), fb.Model
}

// markFailover attributes the served row to the fallback target and records the
// failover for visibility. The requested provider/model stay on the decision record.
func markFailover(c *gin.Context, rm *reqMeta, target schemas.ModelProvider, model string, primaryStatus int) {
	rm.provider = target
	rm.model = model
	rm.decision.Reason = routing.ReasonFallback
	rm.failover = map[string]any{
		"from":           rm.decision.RequestedProvider,
		"to":             string(target),
		"primary_status": primaryStatus,
	}
	c.Writer.Header().Set("x-nb-llm-failover",
		fmt.Sprintf("%s->%s", rm.decision.RequestedProvider, target))
}

// tryFailoverUnary attempts each configured fallback for a native unary request whose
// primary failed transiently, translating the client request to the fallback target
// and re-encoding the response to the client's native shape. Returns true if a
// fallback succeeded (response written + metered). The primary is left untouched.
func (h *handler) tryFailoverUnary(c *gin.Context, bctx *schemas.BifrostContext, rm *reqMeta, primaryStatus int) bool {
	if !isRetryable(primaryStatus) || len(rm.decision.Fallbacks) == 0 {
		return false
	}
	client := schemas.ModelProvider(rm.decision.RequestedProvider)
	for _, fb := range rm.decision.Fallbacks {
		target, model := failoverTarget(rm.decision.RequestedProvider, fb)
		if model == "" {
			continue
		}
		if h.attemptTranslatedUnary(c, bctx, rm, client, target, model, primaryStatus) {
			return true
		}
	}
	return false
}

// attemptTranslatedUnary runs ONE fallback attempt via the unified engine and writes
// the re-encoded response on success. On any failure it returns false WITHOUT writing,
// so the caller can try the next fallback (or surface the original primary error).
func (h *handler) attemptTranslatedUnary(c *gin.Context, bctx *schemas.BifrostContext, rm *reqMeta, client, target schemas.ModelProvider, model string, primaryStatus int) bool {
	var out []byte
	var usage *schemas.BifrostPassthroughUsage

	if client == schemas.OpenAI {
		var oaiReq openai.OpenAIChatRequest
		if json.Unmarshal(rm.body, &oaiReq) != nil {
			return false
		}
		breq := oaiReq.ToBifrostChatRequest(bctx)
		breq.Provider, breq.Model = target, model
		resp, bErr := h.client.ChatCompletionRequest(bctx, breq)
		if bErr != nil || resp == nil {
			return false
		}
		b, err := marshalOpenAIChat(resp)
		if err != nil {
			return false
		}
		out, usage = b, chatUsage(resp)
	} else {
		breq, ok := parseToResponses(client, rm.body, bctx)
		if !ok {
			return false
		}
		breq.Provider, breq.Model = target, model
		resp, bErr := h.client.ResponsesRequest(bctx, breq)
		if bErr != nil || resp == nil {
			return false
		}
		b, err := encodeResponsesUnary(client, bctx, resp)
		if err != nil {
			return false
		}
		out, usage = b, responsesUsage(resp)
	}

	markFailover(c, rm, target, model, primaryStatus)
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(out)))
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(out)
	h.meter(context.WithoutCancel(c.Request.Context()), rm, http.StatusOK, nil, usage, out)
	return true
}

// tryFailoverStream attempts a fallback for a native streaming request whose primary
// failed BEFORE any bytes were sent (a stream can't fail over once the 200 header is
// out). It reuses the streaming translate path for the first usable fallback and
// returns true if it took over (the delegated path writes + meters). Streaming
// commits to one fallback: once its stream opens, we can't peel back to the next.
func (h *handler) tryFailoverStream(c *gin.Context, bctx *schemas.BifrostContext, cancel func(), rm *reqMeta, primaryStatus int) bool {
	if !isRetryable(primaryStatus) || len(rm.decision.Fallbacks) == 0 {
		return false
	}
	client := schemas.ModelProvider(rm.decision.RequestedProvider)
	for _, fb := range rm.decision.Fallbacks {
		target, model := failoverTarget(rm.decision.RequestedProvider, fb)
		if model == "" {
			continue
		}
		rc := &RequestContext{Gin: c, Ctx: c.Request.Context(), Bctx: bctx, Provider: client, Body: rm.body, Streaming: true}

		if client == schemas.OpenAI {
			var oaiReq openai.OpenAIChatRequest
			if json.Unmarshal(rm.body, &oaiReq) != nil {
				continue
			}
			breq := oaiReq.ToBifrostChatRequest(bctx)
			breq.Provider, breq.Model = target, model
			markFailover(c, rm, target, model, primaryStatus)
			h.streamChatWith(c, bctx, cancel, rm, breq)
			return true
		}
		breq, ok := parseToResponses(client, rm.body, bctx)
		if !ok {
			continue
		}
		breq.Provider, breq.Model = target, model
		markFailover(c, rm, target, model, primaryStatus)
		h.streamResponses(c, bctx, cancel, rc, rm, breq)
		return true
	}
	return false
}
