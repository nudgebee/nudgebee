// Package proxy is the gateway's provider-native HTTP edge. It receives
// provider-native requests (/anthropic, /openai, /genai), wraps them as a
// Bifrost passthrough request, and calls the embedded Bifrost core, which does
// the actual native passthrough to the provider. Original request/response bytes
// are forwarded unchanged; streaming (SSE) is flushed per chunk so token-by-token
// responses reach the client without buffering. The gateway edge observes and
// governs (auth/metering/rate-limit); it does NOT translate.
package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/config"
	"nudgebee/llm-gateway/metering"
	"nudgebee/llm-gateway/ratelimit"
	"nudgebee/llm-gateway/routing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// prefixProvider maps each mounted provider-native prefix to the Bifrost
// provider it targets. The prefix is stripped before the request is forwarded,
// so /anthropic/v1/messages becomes a passthrough of /v1/messages to Anthropic.
var prefixProvider = map[string]schemas.ModelProvider{
	"/anthropic": schemas.Anthropic,
	"/openai":    schemas.OpenAI,
	"/genai":     schemas.Gemini, // Google AI (API-key); Vertex is a separate GCP path
}

// prefixStrip lists what to strip from the inbound path before forwarding, per
// mount prefix (first match wins, most-specific first). Anthropic/OpenAI base
// URLs carry no version, so only the mount prefix is stripped. Gemini's base URL
// already includes /v1beta, so the version segment must be stripped too — else
// core builds .../v1beta/v1beta/... and the provider 404s (matches Bifrost's own
// genai passthrough StripPrefix contract).
var prefixStrip = map[string][]string{
	"/anthropic": {"/anthropic"},
	"/openai":    {"/openai"},
	"/genai":     {"/genai/v1beta1", "/genai/v1beta", "/genai/v1", "/genai"},
}

// stripPath removes the provider mount prefix (and version, for genai) from the
// inbound path, yielding the path core appends to the provider base URL.
func stripPath(fullPath, mount string) string {
	for _, p := range prefixStrip[mount] {
		if strings.HasPrefix(fullPath, p) {
			return strings.TrimPrefix(fullPath, p)
		}
	}
	return strings.TrimPrefix(fullPath, mount)
}

// hopByHopResponseHeaders must not be copied from the provider response back to
// the client; the edge sets streaming/transport headers explicitly.
var hopByHopResponseHeaders = map[string]struct{}{
	"connection": {}, "transfer-encoding": {}, "content-length": {},
	"set-cookie": {}, "proxy-authenticate": {}, "www-authenticate": {},
}

// RegisterRoutes mounts the passthrough edge under each provider prefix, backed
// by the embedded Bifrost core client. The auth middleware runs first (resolving
// identity / rejecting bad tokens); sink receives one usage event per request;
// bodyLog stores full bodies when body logging is enabled (off by default).
func RegisterRoutes(r *gin.Engine, client *bifrost.Bifrost, sink metering.Sink, bodyLog metering.BodyLogSink, validator auth.Validator, router routing.Resolver, limiter *ratelimit.Limiter, pricer *metering.Pricer, creds CredResolver) {
	if creds == nil {
		creds = noopCredResolver{}
	}
	h := &handler{
		client:  client,
		sink:    sink,
		bodyLog: bodyLog,
		limiter: limiter,
		pricer:  pricer,
		// Request pipeline (control stages), run after auth and before passthrough.
		// route → ratelimit → resolver → phi. This is the OSS/EE seam: each stage is
		// pluggable, so an OSS build can register no-op defaults later.
		pipeline: NewPipeline(
			routeStage{router: router},
			ratelimitStage{limiter: limiter},
			resolverStage{creds: creds},
			phiStage{},
		),
	}
	// Auth runs first (the pipeline keys off the resolved identity), then the handler
	// runs the pipeline and forwards.
	chain := []gin.HandlerFunc{auth.Middleware(validator), h.handle}
	for prefix := range prefixProvider {
		r.Any(prefix+"/*path", chain...)
	}
}

type handler struct {
	client   *bifrost.Bifrost
	sink     metering.Sink
	bodyLog  metering.BodyLogSink
	limiter  *ratelimit.Limiter
	pricer   *metering.Pricer
	pipeline *Pipeline
}

func (h *handler) handle(c *gin.Context) {
	start := time.Now()
	prefix := "/" + strings.SplitN(strings.TrimPrefix(c.Request.URL.Path, "/"), "/", 2)[0]
	provider, ok := prefixProvider[prefix]
	if !ok {
		writeJSONError(c, http.StatusNotFound, "unknown_provider", "no provider mounted at "+prefix)
		return
	}

	body, err := readBody(c)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSONError(c, http.StatusRequestEntityTooLarge, "request_too_large",
				"request body exceeds the gateway limit")
			return
		}
		writeJSONError(c, http.StatusBadRequest, "invalid_request", "could not read request body")
		return
	}

	logRequestProvenance(c, body)

	path := stripPath(c.Request.URL.Path, prefix) // e.g. /v1/messages, or /models/x:generateContent
	model, bodyStream := parseBody(body)
	if model == "" {
		model = modelFromPath(provider, path) // Gemini/GET carry the model in the path
	}
	// Streaming is signalled either by a body flag (Anthropic/OpenAI: "stream":true)
	// or by the path (Gemini: ...:streamGenerateContent).
	streaming := bodyStream || strings.Contains(strings.ToLower(path), "stream")

	identity := auth.FromContext(c)

	// The Bifrost context carries per-request state; the resolver stage sets a
	// per-tenant credential on it, so it must exist before the pipeline runs.
	bctx, cancel := schemas.NewBifrostContextWithCancel(c.Request.Context())

	// Run the control pipeline (route → ratelimit → resolver → phi). Stages may
	// rewrite the request shape, inject a tenant credential, or reject the request.
	rc := &RequestContext{
		Gin: c, Ctx: c.Request.Context(), Bctx: bctx,
		Identity: identity, Provider: provider,
		Model: model, Path: path, Body: body, Streaming: streaming,
	}
	if stop, err := h.pipeline.Run(rc); err != nil {
		cancel()
		slog.Error("proxy: request pipeline error", "error", err, "provider", provider, "path", path)
		writeJSONError(c, http.StatusInternalServerError, "gateway_error", "request pipeline error")
		return
	} else if stop {
		cancel() // a stage already wrote the rejection (e.g. 429)
		return
	}

	req := &schemas.BifrostPassthroughRequest{
		Provider:    provider,
		Model:       rc.Model,
		Method:      c.Request.Method,
		Path:        rc.Path,
		RawQuery:    c.Request.URL.RawQuery,
		Body:        rc.Body,
		SafeHeaders: safeHeaders(c.Request.Header),
	}

	sessionID, sessionSource := resolveSession(c, rc.Body)
	rm := &reqMeta{
		reqID:         uuid.NewString(),
		provider:      provider,
		req:           req,
		streaming:     streaming,
		start:         start,
		sessionID:     sessionID,
		sessionSource: sessionSource,
		reqAttrs:      extractRequestAttributes(provider, rc.Body),
		identity:      identity,
		decision:      rc.Decision,
	}

	if streaming {
		h.stream(c, bctx, cancel, rm)
		return
	}
	defer cancel()
	h.unary(c, bctx, rm)
}

// reqMeta carries everything the metering step needs about a request, so it can
// be recorded on any exit path without threading many params.
type reqMeta struct {
	reqID         string
	provider      schemas.ModelProvider
	req           *schemas.BifrostPassthroughRequest
	streaming     bool
	start         time.Time
	sessionID     string
	sessionSource string
	reqAttrs      map[string]any
	identity      auth.Identity
	decision      routing.Decision
}

func (h *handler) unary(c *gin.Context, bctx *schemas.BifrostContext, rm *reqMeta) {
	resp, bErr := h.client.Passthrough(bctx, rm.provider, rm.req)
	if bErr != nil {
		status := writeBifrostError(c, bErr)
		h.meter(context.WithoutCancel(c.Request.Context()), rm, status, nil, nil, nil)
		return
	}
	for k, v := range resp.Headers {
		if _, hop := hopByHopResponseHeaders[strings.ToLower(k)]; !hop {
			c.Writer.Header().Set(k, v)
		}
	}
	// The body is complete and uncompressed (we strip accept-encoding), so set an
	// accurate Content-Length rather than dropping the upstream one and forcing
	// chunked encoding — keeps the response byte-faithful for strict clients.
	c.Writer.Header().Set("Content-Length", strconv.Itoa(len(resp.Body)))
	c.Writer.WriteHeader(resp.StatusCode)
	_, _ = c.Writer.Write(resp.Body)
	// Detach from request cancellation (the response is sent) but keep tracing/logger
	// values so the post-response reconcile stays on the request's trace.
	h.meter(context.WithoutCancel(c.Request.Context()), rm, resp.StatusCode, resp.Headers, resp.PassthroughUsage, resp.Body)
}

func (h *handler) stream(c *gin.Context, bctx *schemas.BifrostContext, cancel func(), rm *reqMeta) {
	defer cancel()

	// Meter exactly once, on every exit path (including pre-header failures), so
	// all traffic is recorded. Populated as we learn status/usage; defaults record
	// a failed row if we return before receiving a valid first chunk.
	status := http.StatusBadGateway
	var meterHeaders map[string]string
	var lastUsage *schemas.BifrostPassthroughUsage
	// When body logging is on, accumulate the streamed response (capped) so it can
	// be stored; nil otherwise (streaming response-side attributes come later).
	captureBody := metering.BodyLoggingEnabled()
	var respBuf []byte
	defer func() {
		h.meter(context.WithoutCancel(c.Request.Context()), rm, status, meterHeaders, lastUsage, respBuf)
	}()

	stream, bErr := h.client.PassthroughStream(bctx, rm.provider, rm.req)
	if bErr != nil {
		status = writeBifrostError(c, bErr)
		return
	}

	first, ok := <-stream
	if !ok || first == nil {
		writeJSONError(c, http.StatusBadGateway, "upstream_error", "passthrough stream ended before headers")
		return
	}
	if first.BifrostError != nil {
		status = writeBifrostError(c, first.BifrostError)
		return
	}
	head := first.BifrostPassthroughResponse
	if head == nil {
		writeJSONError(c, http.StatusBadGateway, "upstream_error", "passthrough stream returned empty first chunk")
		return
	}
	// Valid first chunk: from here the client receives head.StatusCode. Usage may
	// arrive on this chunk or a later one; meter reflects the status the client got.
	status = head.StatusCode
	meterHeaders = head.Headers
	lastUsage = head.PassthroughUsage

	// Preserve upstream Content-Type (passthrough streams aren't always SSE); set
	// streaming invariants explicitly and copy the rest.
	contentType := "text/event-stream"
	for k, v := range head.Headers {
		if strings.EqualFold(k, "content-type") {
			contentType = v
			continue
		}
		if _, hop := hopByHopResponseHeaders[strings.ToLower(k)]; !hop {
			c.Writer.Header().Set(k, v)
		}
	}
	c.Writer.Header().Set("Content-Type", contentType)
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(head.StatusCode)

	flusher, _ := c.Writer.(http.Flusher)
	writeChunk := func(b []byte) bool {
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

	if captureBody {
		respBuf = appendCappedBody(respBuf, head.Body)
	}
	if !writeChunk(head.Body) {
		return // client gone; deferred meter records what we have
	}
	for chunk := range stream {
		if chunk == nil {
			continue
		}
		if chunk.BifrostError != nil {
			break // mid-stream error; status stays the 200 the client already received
		}
		if r := chunk.BifrostPassthroughResponse; r != nil {
			if r.PassthroughUsage != nil {
				lastUsage = r.PassthroughUsage
			}
			if captureBody {
				respBuf = appendCappedBody(respBuf, r.Body)
			}
			if !writeChunk(r.Body) {
				return
			}
		}
	}
}

// appendCappedBody appends b to buf until the configured body-log cap, then stops
// (bounds memory for large streams; the stored body carries a truncation marker).
func appendCappedBody(buf, b []byte) []byte {
	if max := config.Config.BodyMaxBytes; max > 0 && int64(len(buf)) >= max {
		return buf
	}
	return append(buf, b...)
}

// meter builds and enqueues one usage event for a completed request. respBody is
// the unary response body for response-side attribute extraction (nil for
// streaming/error paths).
func (h *handler) meter(ctx context.Context, rm *reqMeta, status int, headers map[string]string, usage *schemas.BifrostPassthroughUsage, respBody []byte) {
	var respAttrs map[string]any
	if len(respBody) > 0 {
		respAttrs = extractResponseAttributes(respBody)
	}
	attrs := buildAttributes(rm.provider, rm.req.Model, rm.sessionSource, rm.reqAttrs, respAttrs, usage)
	// The routing decision is the canonical DERIVED signal (see attributes design).
	if rm.decision.Reason != routing.ReasonPassthrough {
		if attrs == nil {
			attrs = &metering.Attributes{Derived: map[string]any{}}
		}
		if attrs.Derived == nil {
			attrs.Derived = map[string]any{}
		}
		attrs.Derived["routing"] = routingAttr(rm.decision)
	}

	ev := metering.NewEvent(metering.EventInput{
		ID:         rm.reqID,
		Provider:   rm.provider,
		Model:      rm.req.Model,
		Method:     rm.req.Method,
		Path:       rm.req.Path,
		Streaming:  rm.streaming,
		StatusCode: status,
		LatencyMS:  time.Since(rm.start).Milliseconds(),
		Headers:    headers,
		Usage:      usage,
		SessionID:  rm.sessionID,
		Attributes: attrs,
		TenantID:   rm.identity.TenantID,
		AccountID:  rm.identity.AccountID,
		UserID:     rm.identity.UserID,
		TokenID:    rm.identity.TokenID,

		RequestedProvider: rm.decision.RequestedProvider,
		RequestedModel:    rm.decision.RequestedModel,
		RoutingReason:     string(rm.decision.Reason),
		RoutingRule:       rm.decision.RuleID,
	})
	h.sink.Record(ev)

	// Reconcile token/cost usage against quotas (requests were counted pre-call).
	if h.limiter.Enabled() && ev.TotalTokens > 0 {
		cost := h.pricer.CostUSD(rm.req.Model, ev.InputTokens, ev.OutputTokens, ev.CacheReadTokens, ev.CacheWriteTokens)
		h.limiter.Reconcile(ctx, ratelimit.Scope{TenantID: rm.identity.TenantID, UserID: rm.identity.UserID}, ev.TotalTokens, cost)
	}

	// Full-body logging (off by default). Linked to the usage row via reqID.
	if metering.BodyLoggingEnabled() {
		now := time.Now().UTC()
		h.bodyLog.Record(metering.BodyLog{
			ID:           uuid.NewString(),
			RequestID:    rm.reqID,
			CreatedAt:    now,
			ExpiresAt:    now.Add(metering.TTL()),
			TenantID:     rm.identity.TenantID,
			UserID:       rm.identity.UserID,
			SessionID:    rm.sessionID,
			Provider:     string(rm.provider),
			Model:        rm.req.Model,
			Method:       rm.req.Method,
			Path:         rm.req.Path,
			StatusCode:   status,
			RequestBody:  metering.CapBody(rm.req.Body),
			ResponseBody: metering.CapBody(respBody),
		})
	}
}

// readBody reads and returns the raw request body, bounded by the configured
// max so a large upload can't exhaust memory. Exceeding the limit yields an
// *http.MaxBytesError, surfaced to the client as 413.
func readBody(c *gin.Context) ([]byte, error) {
	if c.Request.Body == nil {
		return nil, nil
	}
	defer func() { _ = c.Request.Body.Close() }()
	limited := http.MaxBytesReader(c.Writer, c.Request.Body, config.Config.MaxRequestBodyBytes)
	return io.ReadAll(limited)
}

// provenanceValueHeaders are non-sensitive client headers whose VALUES are safe
// to log at debug level, to discover what correlation/session signals clients
// send. api-key/authorization/cookie are never logged (only their presence).
var provenanceValueHeaders = map[string]struct{}{
	"user-agent": {}, "anthropic-version": {}, "anthropic-beta": {},
	"x-stainless-lang": {}, "x-stainless-runtime": {}, "x-stainless-package-version": {},
	"x-stainless-os": {}, "x-stainless-arch": {}, "x-stainless-runtime-version": {},
	"x-app": {}, "x-session-id": {}, "x-nb-session-id": {}, "x-conversation-id": {},
}

// logRequestProvenance logs, at debug level only, what a client sends that could
// correlate requests into a session: header names (all), safe header values, and
// whether the body carries Anthropic's metadata.user_id. Never logs credentials
// or message content. No-op unless LOG_LEVEL=debug.
func logRequestProvenance(c *gin.Context, body []byte) {
	if !slog.Default().Enabled(c.Request.Context(), slog.LevelDebug) {
		return
	}
	headerNames := make([]string, 0, len(c.Request.Header))
	safeValues := map[string]string{}
	for k, v := range c.Request.Header {
		lk := strings.ToLower(k)
		headerNames = append(headerNames, lk)
		if _, ok := provenanceValueHeaders[lk]; ok && len(v) > 0 {
			safeValues[lk] = v[0]
		}
	}
	sort.Strings(headerNames)

	slog.Debug("proxy: request provenance",
		"path", c.Request.URL.Path,
		"header_names", headerNames,
		"header_values", safeValues,
		"body_metadata_user_id", bodyMetadataUserID(body),
	)
}

// parseBody extracts the model and stream flag from a provider-native JSON body.
// Both are best-effort hints: model feeds key selection (bypassed when the plugin
// injects a key) and stream selects the streaming path.
func parseBody(body []byte) (model string, stream bool) {
	if len(body) == 0 {
		return "", false
	}
	var m struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &m)
	return m.Model, m.Stream
}

// safeHeaders copies client headers to forward, stripping auth, hop-by-hop, and
// internal gateway (x-bf-*) headers. The provider credential is injected by the
// engine, so inbound auth headers must not leak upstream.
func safeHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		lk := strings.ToLower(k)
		switch lk {
		case "authorization", "api-key", "x-api-key", "x-goog-api-key",
			"host", "connection", "transfer-encoding", "cookie", "set-cookie",
			"proxy-authorization", "accept-encoding", "content-length":
			continue
		}
		if strings.HasPrefix(lk, "x-bf-") {
			continue
		}
		if len(vs) > 0 {
			out[lk] = vs[0]
		}
	}
	return out
}

// writeBifrostError writes a JSON error and returns the HTTP status used (for metering).
func writeBifrostError(c *gin.Context, bErr *schemas.BifrostError) int {
	status := http.StatusBadGateway
	if bErr.StatusCode != nil {
		status = *bErr.StatusCode
	}
	msg := "provider engine error"
	if bErr.Error != nil && bErr.Error.Message != "" {
		msg = bErr.Error.Message
	}
	writeJSONError(c, status, "upstream_error", msg)
	return status
}

func writeJSONError(c *gin.Context, status int, typ, msg string) {
	c.JSON(status, gin.H{"error": gin.H{"type": typ, "message": msg}})
}
