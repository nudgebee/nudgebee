package egressfilter

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tmc/langchaingo/llms"
)

// WrapModel returns model unchanged when the egressfilter is disabled, or a
// scrubbing decorator when it is enabled. The wrapper implements llms.Model
// and is transparent to every caller — only GenerateContent and Call are
// instrumented.
//
// provider and modelName are baked into the wrapper so they can be attached as
// metric labels and structured-log fields without needing to thread them
// through the langchaingo CallOption tree.
func WrapModel(model llms.Model, provider, modelName string, enabled bool, mode Mode) llms.Model {
	if !enabled || model == nil {
		return model
	}
	return &wrappedModel{
		inner:    model,
		provider: provider,
		model:    modelName,
		mode:     mode,
	}
}

// wrappedModel is the llms.Model decorator that runs Scan before delegating.
type wrappedModel struct {
	inner    llms.Model
	provider string
	model    string
	mode     Mode
}

// GenerateContent is the primary entrypoint. We serialize the message slice
// into a single string plus a parallel slice of source regions (so each Hit
// can be tagged with the kind of part it came from), scan it, and either
// block, redact, or pass through.
//
// When scanAndDecide returns a non-nil mutated slice, that slice is
// forwarded to the inner model instead of the caller's messages —
// redact mode replaces hit ranges with placeholders so the provider
// never sees the raw secret.
func (w *wrappedModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	payload, regions := serializeMessagesWithSources(messages)
	mutated, err := w.scanAndDecide(ctx, payload, regions, messages)
	if err != nil {
		return nil, err
	}
	outbound := messages
	if mutated != nil {
		outbound = mutated
	}
	return w.inner.GenerateContent(ctx, outbound, options...)
}

// Call satisfies the deprecated single-prompt entry on llms.Model. It routes
// through GenerateFromSinglePrompt with the wrapper itself as the model so the
// scan runs exactly once via GenerateContent — same pattern used by every
// provider in this repo.
func (w *wrappedModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, w, prompt, options...)
}

// scanAndDecide runs Scan + policy resolution against the serialized
// payload and returns either an error (blocked call), a mutated messages
// slice to forward instead of the caller's (redact mode), or (nil, nil)
// meaning the caller should forward its original messages unchanged
// (detect / clean / skipped paths).
func (w *wrappedModel) scanAndDecide(
	ctx context.Context,
	payload string,
	regions []SourceRegion,
	messages []llms.MessageContent,
) ([]llms.MessageContent, error) {
	start := time.Now()

	// Resolve per-tenant overrides (nil → env defaults apply).
	tcfg := Resolve(ctx)
	if tcfg != nil && !tcfg.Enabled {
		// Per-tenant kill switch: skip scan entirely for this tenant.
		recordScan(ctx, w.provider, w.model, w.mode, "skipped", len(payload), 0, nil)
		return nil, nil
	}

	result := Scan(payload)
	// Tag hits with their source kind so dashboards and (future) policy
	// can distinguish user-typed secrets from agents reading configs. Done
	// before tenant overrides so the Source survives onto FilterEvent hits.
	result.Hits = tagHitsBySource(result.Hits, regions)
	result = applyTenantOverrides(result, payload, tcfg)
	latency := time.Since(start).Seconds()

	if !result.HasHits() {
		recordScan(ctx, w.provider, w.model, w.mode, "clean", len(payload), latency, nil)
		return nil, nil
	}

	// Effective mode: tenant override wins over the wrapper-constructed
	// default (which is env-level). Empty tenant Mode → use env.
	effectiveMode := w.mode
	if tcfg != nil && tcfg.Mode != "" {
		effectiveMode = tcfg.Mode
	}

	ruleIDs := result.RuleIDs()
	auditID := newAuditID()
	agentName, _ := AgentNameFromContext(ctx)
	// Build the structured event once; the reporter (if any) and the
	// returned typed Error share these fields. Agent name is surfaced on
	// the event for downstream dashboard queries (and the future per-agent
	// policy phase).
	event := newFilterEvent(auditID, effectiveMode, len(payload), result, agentName)

	// Mode is operator config; Action is what we actually do. The gate
	// indirection lets a deployment plug in its own post-detection policy
	// — see action_gate.go.
	switch resolveAction(effectiveMode, result) {
	case ActionBlock:
		recordScan(ctx, w.provider, w.model, effectiveMode, "blocked", len(payload), latency, result.Hits)
		err := &Error{AuditID: auditID, RuleIDs: ruleIDs}
		slog.Warn("egressfilter: outbound LLM call blocked",
			"audit_id", auditID,
			"provider", w.provider,
			"model", w.model,
			"rule_ids", ruleIDs,
			"hits", len(result.Hits),
			"payload_bytes", len(payload),
			"latency_seconds", latency,
		)
		if fn := reporterFromContext(ctx); fn != nil {
			fn(event)
		}
		return nil, fmt.Errorf("egressfilter: %w", err)

	case ActionRedact:
		// Delegate to the installed Redactor plugin (typically registered
		// by the EE bundle). If no Redactor is registered — an OSS-only
		// build, or a misconfigured init order — degrade to detect
		// behaviour with a one-shot warn so the operator sees the gap.
		if Redactor == nil {
			warnRedactWithoutRedactor()
			recordScan(ctx, w.provider, w.model, effectiveMode, "detect", len(payload), latency, result.Hits)
			slog.Warn("egressfilter: outbound LLM payload contains potential secret (redact requested but no Redactor registered; falling back to detect)",
				"audit_id", auditID,
				"provider", w.provider,
				"model", w.model,
				"rule_ids", ruleIDs,
				"hits", len(result.Hits),
				"payload_bytes", len(payload),
				"latency_seconds", latency,
			)
			if fn := reporterFromContext(ctx); fn != nil {
				fn(event)
			}
			return nil, nil
		}
		mutated, redactions := Redactor(messages, result.Hits, regions)
		event.Redactions = redactions
		recordScan(ctx, w.provider, w.model, effectiveMode, "redacted", len(payload), latency, result.Hits)
		slog.Warn("egressfilter: outbound LLM payload redacted",
			"audit_id", auditID,
			"provider", w.provider,
			"model", w.model,
			"rule_ids", ruleIDs,
			"hits", len(result.Hits),
			"payload_bytes", len(payload),
			"latency_seconds", latency,
		)
		if fn := reporterFromContext(ctx); fn != nil {
			fn(event)
		}
		return mutated, nil

	default:
		// ActionAudit (and any future non-blocking action): record + log,
		// forward unchanged.
		recordScan(ctx, w.provider, w.model, effectiveMode, "detect", len(payload), latency, result.Hits)
		slog.Warn("egressfilter: outbound LLM payload contains potential secret (detect mode, not blocking)",
			"audit_id", auditID,
			"provider", w.provider,
			"model", w.model,
			"rule_ids", ruleIDs,
			"hits", len(result.Hits),
			"payload_bytes", len(payload),
			"latency_seconds", latency,
		)
		if fn := reporterFromContext(ctx); fn != nil {
			fn(event)
		}
		return nil, nil
	}
}

// serializeMessages flattens a langchaingo message slice into the smallest text
// blob that preserves all human-readable content. Thin wrapper kept for
// back-compat with tests that don't care about source regions.
func serializeMessages(messages []llms.MessageContent) string {
	s, _ := serializeMessagesWithSources(messages)
	return s
}

// serializeMessagesWithSources flattens a langchaingo message slice into the
// scan payload AND a parallel slice of source regions so each Hit can be
// tagged with the kind of part it came from. Detection only needs to see
// the concatenated text; structural boundaries are irrelevant for the
// regex pass. Raw binary content (e.g. llms.BinaryContent) is skipped
// because secret regexes can't match it and feeding raw bytes through
// would only waste cycles.
//
// llms.ImageURLContent IS scanned (its URL field): pre-signed S3 URLs and
// Azure Blob SAS tokens routinely carry credential material in query
// params, and we don't want secrets to slip through just because they ride
// on an image part.
//
// Both value and pointer forms of each part type are handled — langchaingo
// callers (and our own llms/* providers) inconsistently use either. Missing
// a pointer form would let secrets in tool calls slip past the egressfilter.
//
// Region boundaries: each region covers [partStart, partEndIncludingNewline)
// so the regions tile the entire payload, and a hit whose Start lands on
// the terminating \n of a part is still attributed to that part.
func serializeMessagesWithSources(messages []llms.MessageContent) (string, []SourceRegion) {
	var b strings.Builder
	var regions []SourceRegion

	// Hoisted so we allocate one closure for the whole call instead of
	// one per (msg, part) iteration — GenerateContent is on the hot path
	// of every LLM call. currentMIdx / currentPIdx are updated at the top
	// of each iteration and read by the closure via capture.
	var currentMIdx, currentPIdx int
	addRegion := func(text string, src Source) {
		if text == "" {
			return
		}
		start := b.Len()
		b.WriteString(text)
		b.WriteByte('\n')
		regions = append(regions, SourceRegion{
			Start: start, End: b.Len(), Source: src,
			MsgIdx: currentMIdx, PartIdx: currentPIdx,
		})
	}

	for mIdx, m := range messages {
		textSource := sourceForRole(m.Role)
		for pIdx, p := range m.Parts {
			currentMIdx = mIdx
			currentPIdx = pIdx
			switch part := p.(type) {
			case llms.TextContent:
				addRegion(part.Text, textSource)
			case *llms.TextContent:
				if part != nil {
					addRegion(part.Text, textSource)
				}
			case llms.ToolCall:
				if part.FunctionCall != nil {
					addRegion(part.FunctionCall.Arguments, SourceToolCallArgs)
				}
			case *llms.ToolCall:
				if part != nil && part.FunctionCall != nil {
					addRegion(part.FunctionCall.Arguments, SourceToolCallArgs)
				}
			case llms.ToolCallResponse:
				addRegion(part.Content, SourceToolResult)
			case *llms.ToolCallResponse:
				if part != nil {
					addRegion(part.Content, SourceToolResult)
				}
			case llms.ImageURLContent:
				addRegion(part.URL, SourceImageURL)
			case *llms.ImageURLContent:
				if part != nil {
					addRegion(part.URL, SourceImageURL)
				}
			}
		}
	}
	return b.String(), regions
}

// sourceForRole maps a langchaingo chat-message role to the Source we
// assign to its TextContent parts. Tool/Image/ToolCall parts are tagged by
// their own type regardless of role.
func sourceForRole(role llms.ChatMessageType) Source {
	switch role {
	case llms.ChatMessageTypeSystem:
		return SourceSystem
	case llms.ChatMessageTypeHuman:
		return SourceUser
	case llms.ChatMessageTypeAI:
		return SourceAssistant
	case llms.ChatMessageTypeTool:
		return SourceToolResult
	default:
		// Unknown role: default to user — conservative, surfaces in dashboards.
		return SourceUser
	}
}

// newAuditID returns a short request-scoped identifier suitable for surfacing
// to end users. It is intentionally not tied to trace IDs: egressfilter events may
// occur outside of a request span, and we want the ID to be self-contained.
//
// We take 12 hex chars (48 bits) of a fresh UUIDv4: 32 bits hits 50% birthday
// collision around ~65K IDs, which is reachable in a busy hour and would make
// log correlation ambiguous; 48 bits pushes that boundary out past ~16M IDs
// while still fitting comfortably in a user-facing error string.
func newAuditID() string {
	id := strings.ReplaceAll(uuid.New().String(), "-", "")
	return "egress-" + id[:12]
}
