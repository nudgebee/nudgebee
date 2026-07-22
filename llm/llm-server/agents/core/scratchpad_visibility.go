package core

import (
	"fmt"
	"log/slog"
	"strings"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// compressionVisibilityToolName is the synthetic tool name used for compression
// visibility records persisted to the conversation timeline.
const compressionVisibilityToolName = "context_compression"

// compressionVisibilityToolID is a fixed tool ID so the DB upserts a single
// compression summary record per (conversation, message, agent) tuple.
const compressionVisibilityToolID = "compression_summary"

// Compression cause classifications surfaced in the visibility card so the
// reader can tell window-pressure compression (the headline "approached the
// context window" case) apart from the always-on pre-refinement compression
// that fires every time a critique rejection bumps postRefinementToolIndex.
// Pre-#33083, the card title hardcoded the window-pressure phrasing, leading
// to confusing reports of "compressed to fit the context window" on
// 4-step / 7-KB conversations whose only cause was refinement-focus.
const (
	compressionCauseWindowPressure  = "window_pressure"
	compressionCauseRefinementFocus = "refinement_focus"
	compressionCauseUnknown         = "unknown"

	// compressionEventOriginalPreviewBytes bounds how much of the pre-compression
	// observation we keep on the visibility card. Trades resolution for blast
	// radius: a 10-event compression on a tool whose observation is 65KB would
	// inflate the card by ~650KB at full fidelity. 300 chars is enough to
	// recognize the content shape (which tool, which target) for review.
	compressionEventOriginalPreviewBytes = 300
)

// CompressionTracker tracks whether compression visibility has been persisted
// to the conversation DB. It avoids redundant DB writes when the set of
// compressed steps hasn't changed between scratchpad builds.
//
// It also carries the most recent (windowPressureActive, postRefinementToolIndex)
// observation from buildScratchpad so SaveCompressionVisibility can classify
// each compression event's cause without re-deriving it. SetCompressionContext
// is called once per buildScratchpad pass; the tracker keeps the latest values.
type CompressionTracker struct {
	lastReportedCount       int
	windowPressureActive    bool
	postRefinementToolIndex int
}

// NewCompressionTracker creates a tracker for deduplicating compression visibility saves.
func NewCompressionTracker() *CompressionTracker {
	return &CompressionTracker{}
}

// SetCompressionContext records the gating signals from the most recent
// buildScratchpad pass so SaveCompressionVisibility can classify each event.
// Safe on a nil receiver (no-op) for ReWOO/test callers that may pass nil.
func (t *CompressionTracker) SetCompressionContext(windowPressureActive bool, postRefinementToolIndex int) {
	if t == nil {
		return
	}
	t.windowPressureActive = windowPressureActive
	t.postRefinementToolIndex = postRefinementToolIndex
}

// compressionEvent describes a single step's compression for the visibility summary.
type compressionEvent struct {
	toolID          string
	toolName        string
	originalLen     int
	compressedLen   int
	method          string // "llm_summary" or "truncated"
	cause           string // compressionCause*
	originalPreview string // bounded peek at the pre-compression observation
	compressedText  string // the actual compressed output (already short)
}

// collectCompressionEvents scans steps and returns events for all steps that
// have been compressed (i.e. have a non-empty CompressedObservation). Each
// event is classified by cause using the planner-supplied gating signals:
//   - step index < postRefinementToolIndex → refinement-focus (always-on)
//   - else, windowPressureActive → window-pressure
//   - else → unknown (defensive — shouldn't happen post-#33083 unless a new
//     compression path lands without updating SetCompressionContext)
func collectCompressionEvents(steps []NBAgentPlannerToolActionStep, windowPressureActive bool, postRefinementToolIndex int) []compressionEvent {
	var events []compressionEvent
	for i, step := range steps {
		if step.CompressedObservation == "" {
			continue
		}
		method := "truncated"
		if strings.HasPrefix(step.CompressedObservation, "[summarized from") {
			method = "llm_summary"
		}
		cause := compressionCauseUnknown
		switch {
		case i < postRefinementToolIndex:
			cause = compressionCauseRefinementFocus
		case windowPressureActive:
			cause = compressionCauseWindowPressure
		}
		events = append(events, compressionEvent{
			toolID:          step.Action.ToolID,
			toolName:        step.Action.Tool,
			originalLen:     len(step.Observation),
			compressedLen:   len(step.CompressedObservation),
			method:          method,
			cause:           cause,
			originalPreview: TruncateHead(step.Observation, compressionEventOriginalPreviewBytes),
			compressedText:  step.CompressedObservation,
		})
	}
	return events
}

// causeCounts aggregates compression events by cause. Used both for the
// human-readable summary line and for title classification.
type causeCounts struct {
	window     int
	refinement int
	unknown    int
}

func tallyCauses(events []compressionEvent) causeCounts {
	var c causeCounts
	for _, e := range events {
		switch e.cause {
		case compressionCauseWindowPressure:
			c.window++
		case compressionCauseRefinementFocus:
			c.refinement++
		default:
			c.unknown++
		}
	}
	return c
}

// formatCompressionSummary builds a human-readable summary of compression
// events. Each event is rendered with sizes, method, cause, and side-by-side
// before/after snippets so a reviewer can judge whether compression dropped
// information that mattered.
func formatCompressionSummary(events []compressionEvent) string {
	var sb strings.Builder
	sb.WriteString("Context Compression Summary:\n")
	totalOriginal := 0
	totalCompressed := 0
	llmCount := 0
	truncCount := 0
	for _, e := range events {
		id := e.toolID
		if id == "" {
			id = e.toolName
		}
		fmt.Fprintf(&sb, "\n- Step %s (%s): %d → %d chars (%s, %s)\n",
			id, e.toolName, e.originalLen, e.compressedLen, e.method, e.cause)
		if e.originalPreview != "" {
			truncatedNote := ""
			if len(e.originalPreview) < e.originalLen {
				truncatedNote = fmt.Sprintf(" [+%d chars elided]", e.originalLen-len(e.originalPreview))
			}
			fmt.Fprintf(&sb, "  before: %s%s\n", e.originalPreview, truncatedNote)
		}
		if e.compressedText != "" {
			fmt.Fprintf(&sb, "  after:  %s\n", e.compressedText)
		}
		totalOriginal += e.originalLen
		totalCompressed += e.compressedLen
		if e.method == "llm_summary" {
			llmCount++
		} else {
			truncCount++
		}
	}
	reduction := float64(0)
	if totalOriginal > 0 {
		reduction = (1 - float64(totalCompressed)/float64(totalOriginal)) * 100
	}
	fmt.Fprintf(&sb, "\nTotal: %d chars → %d chars (%.1f%% reduction)\n",
		totalOriginal, totalCompressed, reduction)
	fmt.Fprintf(&sb, "Methods: %d LLM summaries, %d byte truncations", llmCount, truncCount)
	counts := tallyCauses(events)
	fmt.Fprintf(&sb, "\nCauses: %d window-pressure, %d refinement-focus", counts.window, counts.refinement)
	if counts.unknown > 0 {
		fmt.Fprintf(&sb, ", %d unknown", counts.unknown)
	}
	return sb.String()
}

// SaveCompressionVisibility persists a synthetic tool call summarizing any
// compressed observations, so compression events appear in the conversation UI.
// It uses a fixed tool ID so the DB upserts (updates) on subsequent calls rather
// than creating duplicate records.
//
// The record is saved under the ParentAgentId (the top-level debug agent like
// k8s_debug) so it renders as its own task card in the UI. The tool_id includes
// the sub-agent's AgentId to avoid upsert collisions across sub-agents.
//
// The tracker ensures we skip the DB write when nothing has changed since the
// last save. Pass nil tracker to always save (one-shot callers like solver/critiquer).
func SaveCompressionVisibility(
	ctx *security.RequestContext,
	request NBAgentRequest,
	steps []NBAgentPlannerToolActionStep,
	tracker *CompressionTracker,
) {
	if ctx == nil || !config.Config.LlmServerScratchpadSummarizationEnabled {
		return
	}

	// Tracker carries the gating signals from the most recent buildScratchpad
	// pass. When absent (one-shot callers or older code paths), default to
	// "unknown" classification — better to surface the ambiguity on the card
	// than to silently claim window pressure was the cause.
	windowPressureActive := false
	postRefinementIdx := 0
	if tracker != nil {
		windowPressureActive = tracker.windowPressureActive
		postRefinementIdx = tracker.postRefinementToolIndex
	}

	events := collectCompressionEvents(steps, windowPressureActive, postRefinementIdx)
	if len(events) == 0 {
		return
	}

	// Dedup: skip DB write if compressed step count hasn't changed.
	if tracker != nil && len(events) == tracker.lastReportedCount {
		return
	}
	if tracker != nil {
		tracker.lastReportedCount = len(events)
	}

	summary := formatCompressionSummary(events)

	// Save under the parent (top-level) agent so the UI renders it as a visible
	// task card alongside other tool calls. Use a per-sub-agent tool_id to avoid
	// upsert collisions when multiple sub-agents compress independently.
	agentID := request.ParentAgentId
	if agentID == "" {
		agentID = request.AgentId
	}
	toolID := compressionVisibilityToolID + "_" + request.AgentId

	counts := tallyCauses(events)
	title, description := compressionCardCopy(len(events), counts)

	err := GetConversationDao().SaveConversationToolCall(
		request.ConversationId,
		request.AccountId,
		request.UserId,
		request.MessageId,
		agentID,
		toolID,
		compressionVisibilityToolName,
		title,
		description,
		"", // sqlArgs
		summary,
		toolcore.NBToolResponseStatusSuccess,
		toolcore.NBToolTypeTool,
		nil,
		nil,
		nil,
	)
	if err != nil {
		ctx.GetLogger().Warn("scratchpad: failed to save compression visibility", "error", err)
	}
}

// compressionCardCopy produces the UI card title + description, tuned to the
// cause mix. Mixed-cause cards lead with the dominant cause and append the
// other count rather than running everything together — easier to skim.
func compressionCardCopy(total int, c causeCounts) (title, description string) {
	switch {
	case c.window > 0 && c.refinement == 0 && c.unknown == 0:
		title = fmt.Sprintf("Compressed %d older observation(s) — scratchpad near model context window", total)
		description = "Older tool observations were summarized or truncated because the scratchpad approached the model's context window. Recent observations are kept in full."
	case c.refinement > 0 && c.window == 0 && c.unknown == 0:
		title = fmt.Sprintf("Compressed %d older observation(s) — kept refined investigation focused", total)
		description = "After the critique asked for a refined answer, observations from before the refinement were summarized so the next pass stays focused on the refined investigation. This happens regardless of context-window usage."
	case c.window > 0 && c.refinement > 0:
		title = fmt.Sprintf("Compressed %d older observation(s) — %d window-pressure, %d refinement-focus", total, c.window, c.refinement)
		description = "Some observations were compressed because the scratchpad approached the model's context window; others were compressed because a critique-driven refinement pushed older steps out of focus. Recent observations are kept in full."
	default:
		title = fmt.Sprintf("Compressed %d older observation(s) — cause not classified", total)
		description = "Compression fired but the cause was not classified (a new compression path may have been added without updating the visibility tracker). See the per-event summary below."
	}
	return title, description
}

// CountCompressionVisibilityRecords returns the number of context_compression
// tool call records for a given conversation. Intended for integration tests.
func CountCompressionVisibilityRecords(conversationId string) int {
	dao, ok := GetConversationDao().(*ConversationDao)
	if !ok || dao == nil {
		return 0
	}
	var count int
	err := dao.dbManager.Db.Get(&count,
		`SELECT COUNT(*) FROM llm_conversation_tool_calls
		 WHERE conversation_id = $1 AND tool_name = $2`,
		conversationId, compressionVisibilityToolName)
	if err != nil {
		slog.Warn("scratchpad: failed to count compression visibility records", "error", err)
		return 0
	}
	return count
}
