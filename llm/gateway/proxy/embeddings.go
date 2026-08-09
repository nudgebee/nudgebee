package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/providers/openai"
	"github.com/maximhq/bifrost/core/schemas"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/edgeerr"
)

// embeddingsPath is the canonical path recorded for generic-endpoint embedding requests.
const embeddingsPath = "/v1/embeddings"

// handleEmbeddings is the tenant-routed, OpenAI-compatible embeddings endpoint. It
// mirrors handleChat's control path — resolve the model to a provider (including a
// tenant's custom OpenAI-compatible upstream), run the governance pipeline (rate
// limits + per-tenant credential resolution), then dispatch through the unified
// engine — but for embeddings, which are always unary. This is the ONLY embeddings
// door for the custom/vLLM lane, which has no native provider prefix.
func (h *handler) handleEmbeddings(c *gin.Context) {
	start := time.Now()

	body, err := readBody(c)
	if err != nil {
		id := auth.FromContext(c)
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			edgeerr.Write(c, edgeerr.OpenAI, http.StatusRequestEntityTooLarge, "request_too_large",
				"request body exceeds the gateway limit")
			h.recordReject(id, schemas.OpenAI, "", c.Request.Method, embeddingsPath, http.StatusRequestEntityTooLarge, "too_large", start)
			return
		}
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusBadRequest, "invalid_request", "could not read request body")
		h.recordReject(id, schemas.OpenAI, "", c.Request.Method, embeddingsPath, http.StatusBadRequest, "bad_request", start)
		return
	}

	logRequestProvenance(c, body)

	identity := auth.FromContext(c)

	requestedModel, _ := parseBody(body) // embeddings are never streamed
	provider, model, ok := resolveModelProvider(requestedModel)
	// customKey, when set, is a per-request credential for a tenant's custom OpenAI-
	// compatible upstream carrying its base URL — injected as a DirectKey so the request
	// routes on the vLLM lane. Tier aliases don't apply to embeddings, so (unlike chat)
	// there's no tier fallback here.
	var customKey *schemas.Key
	if !ok {
		if cp, key, isCustom := resolveCustomProvider(identity.TenantID, requestedModel); isCustom {
			provider, model = cp, requestedModel
			customKey = &key
		} else {
			edgeerr.Write(c, edgeerr.OpenAI, http.StatusBadRequest, "invalid_request",
				`unknown or missing model; address it as "provider/model" (e.g. "openai/text-embedding-3-small") or a known model name`)
			h.recordReject(identity, schemas.OpenAI, requestedModel, c.Request.Method, embeddingsPath, http.StatusBadRequest, "unknown_model", start)
			return
		}
	}

	bctx, cancel := schemas.NewBifrostContextWithCancel(c.Request.Context())
	defer cancel()

	rc := &RequestContext{
		Gin: c, Ctx: c.Request.Context(), Bctx: bctx,
		Identity: identity, Provider: provider,
		Model: model, Path: embeddingsPath, Body: body, Streaming: false,
		DirectKey: customKey,
	}
	if stop, err := h.pipeline.Run(rc); err != nil {
		slog.Error("proxy: embeddings pipeline error", "error", err, "provider", provider, "model", model)
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusInternalServerError, "gateway_error", "request pipeline error")
		h.recordReject(identity, provider, model, c.Request.Method, embeddingsPath, http.StatusInternalServerError, "gateway_error", start)
		return
	} else if stop {
		h.recordRejectPipeline(rc, c.Writer.Status(), start) // a stage already wrote the rejection (429 / no-creds 403)
		return
	}

	rm := &reqMeta{
		reqID:     uuid.NewString(),
		provider:  rc.Provider,
		model:     rc.Model,
		method:    c.Request.Method,
		path:      embeddingsPath,
		body:      rc.Body,
		streaming: false,
		surface:   surfaceGeneric,
		dlp:       rc.DLP,
		start:     start,
		reqAttrs:  extractRequestAttributes(rc.Provider, rc.Body),
		identity:  identity,
		decision:  rc.Decision,
	}
	h.unaryEmbedding(c, bctx, rc, rm)
}

// unaryEmbedding parses the OpenAI embedding body into the unified request, pins the
// routed provider/model, dispatches through the shared engine, and returns the
// canonical OpenAI embedding response. Metering runs on every exit path.
func (h *handler) unaryEmbedding(c *gin.Context, bctx *schemas.BifrostContext, rc *RequestContext, rm *reqMeta) {
	meterCtx := context.WithoutCancel(c.Request.Context())

	var oaiReq openai.OpenAIEmbeddingRequest
	if err := json.Unmarshal(rc.Body, &oaiReq); err != nil {
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusBadRequest, "invalid_request",
			"could not parse request body as an OpenAI embedding request")
		h.meter(meterCtx, rm, http.StatusBadRequest, nil, nil, nil)
		return
	}
	breq := oaiReq.ToBifrostEmbeddingRequest(bctx)
	breq.Provider = rc.Provider
	breq.Model = rc.Model

	resp, bErr := h.client.EmbeddingRequest(bctx, breq)
	if bErr != nil {
		status := writeBifrostError(c, bErr)
		h.meter(meterCtx, rm, status, nil, nil, nil)
		return
	}
	if resp == nil {
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusBadGateway, "upstream_error",
			"empty response from the provider engine")
		h.meter(meterCtx, rm, http.StatusBadGateway, nil, nil, nil)
		return
	}

	rm.respondedModel = resp.Model
	out, err := marshalOpenAIEmbedding(resp)
	if err != nil {
		edgeerr.Write(c, edgeerr.OpenAI, http.StatusInternalServerError, "gateway_error", "could not encode response")
		h.meter(meterCtx, rm, http.StatusInternalServerError, nil, embeddingUsage(resp), nil)
		return
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(out)))
	c.Writer.WriteHeader(http.StatusOK)
	_, _ = c.Writer.Write(out)

	h.meter(meterCtx, rm, http.StatusOK, nil, embeddingUsage(resp), out)
}

// openAIEmbeddingView marshals a unified embedding response in clean OpenAI shape,
// dropping the internal extra_fields block (mirrors openAIChatView).
type openAIEmbeddingView struct {
	*schemas.BifrostEmbeddingResponse
	ExtraFields any `json:"extra_fields,omitempty"`
}

func marshalOpenAIEmbedding(resp *schemas.BifrostEmbeddingResponse) ([]byte, error) {
	return json.Marshal(openAIEmbeddingView{BifrostEmbeddingResponse: resp})
}

// embeddingUsage lifts the response token usage into the metering shape. Embeddings
// carry input/total tokens only (no completion), and the usage type is identical to
// chat's, so the whole meter + pricing path is reused unchanged.
func embeddingUsage(resp *schemas.BifrostEmbeddingResponse) *schemas.BifrostPassthroughUsage {
	if resp == nil {
		return nil
	}
	return &schemas.BifrostPassthroughUsage{LLMUsage: resp.Usage}
}
