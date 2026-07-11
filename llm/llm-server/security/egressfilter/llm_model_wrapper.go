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
// into a single string (a flat concatenation is sufficient for detection — we
// don't need structural fidelity, only content), scan it, and either block or
// pass through.
func (w *wrappedModel) GenerateContent(ctx context.Context, messages []llms.MessageContent, options ...llms.CallOption) (*llms.ContentResponse, error) {
	payload := serializeMessages(messages)
	if err := w.scanAndDecide(ctx, payload); err != nil {
		return nil, err
	}
	return w.inner.GenerateContent(ctx, messages, options...)
}

// Call satisfies the deprecated single-prompt entry on llms.Model. It routes
// through GenerateFromSinglePrompt with the wrapper itself as the model so the
// scan runs exactly once via GenerateContent — same pattern used by every
// provider in this repo.
func (w *wrappedModel) Call(ctx context.Context, prompt string, options ...llms.CallOption) (string, error) {
	return llms.GenerateFromSinglePrompt(ctx, w, prompt, options...)
}

func (w *wrappedModel) scanAndDecide(ctx context.Context, payload string) error {
	start := time.Now()
	result := Scan(payload)
	latency := time.Since(start).Seconds()

	if !result.HasHits() {
		recordScan(ctx, w.provider, w.model, w.mode, "clean", len(payload), latency, nil)
		return nil
	}

	ruleIDs := result.RuleIDs()
	auditID := newAuditID()
	// Build the structured event once; the reporter (if any) and the
	// returned typed Error share these fields.
	event := newFilterEvent(auditID, w.mode, len(payload), result)

	if w.mode == ModeEnforce {
		recordScan(ctx, w.provider, w.model, w.mode, "blocked", len(payload), latency, result.Hits)
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
		return fmt.Errorf("egressfilter: %w", err)
	}

	// Audit mode: record + log, do not block.
	recordScan(ctx, w.provider, w.model, w.mode, "audit", len(payload), latency, result.Hits)
	slog.Warn("egressfilter: outbound LLM payload contains potential secret (audit mode, not blocking)",
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
	return nil
}

// serializeMessages flattens a langchaingo message slice into the smallest text
// blob that preserves all human-readable content. Detection only needs to see
// the concatenated text; structural boundaries are irrelevant. Raw binary
// content (e.g. llms.BinaryContent) is skipped because secret regexes can't
// match it and feeding raw bytes through would only waste cycles.
//
// llms.ImageURLContent IS scanned (its URL field): pre-signed S3 URLs and
// Azure Blob SAS tokens routinely carry credential material in query params,
// and we don't want secrets to slip through just because they ride on an
// image part.
//
// Both value and pointer forms of each part type are handled — langchaingo
// callers (and our own llms/* providers) inconsistently use either. Missing a
// pointer form would let secrets in tool calls slip past the egressfilter.
func serializeMessages(messages []llms.MessageContent) string {
	var b strings.Builder
	for _, m := range messages {
		for _, p := range m.Parts {
			switch part := p.(type) {
			case llms.TextContent:
				b.WriteString(part.Text)
				b.WriteByte('\n')
			case *llms.TextContent:
				if part != nil {
					b.WriteString(part.Text)
					b.WriteByte('\n')
				}
			case llms.ToolCall:
				if part.FunctionCall != nil {
					b.WriteString(part.FunctionCall.Arguments)
					b.WriteByte('\n')
				}
			case *llms.ToolCall:
				if part != nil && part.FunctionCall != nil {
					b.WriteString(part.FunctionCall.Arguments)
					b.WriteByte('\n')
				}
			case llms.ToolCallResponse:
				b.WriteString(part.Content)
				b.WriteByte('\n')
			case *llms.ToolCallResponse:
				if part != nil {
					b.WriteString(part.Content)
					b.WriteByte('\n')
				}
			case llms.ImageURLContent:
				b.WriteString(part.URL)
				b.WriteByte('\n')
			case *llms.ImageURLContent:
				if part != nil {
					b.WriteString(part.URL)
					b.WriteByte('\n')
				}
			}
		}
	}
	return b.String()
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
