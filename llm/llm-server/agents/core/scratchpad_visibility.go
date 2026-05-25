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

// CompressionTracker tracks whether compression visibility has been persisted
// to the conversation DB. It avoids redundant DB writes when the set of
// compressed steps hasn't changed between scratchpad builds.
type CompressionTracker struct {
	lastReportedCount int
}

// NewCompressionTracker creates a tracker for deduplicating compression visibility saves.
func NewCompressionTracker() *CompressionTracker {
	return &CompressionTracker{}
}

// compressionEvent describes a single step's compression for the visibility summary.
type compressionEvent struct {
	toolID        string
	toolName      string
	originalLen   int
	compressedLen int
	method        string // "llm_summary" or "truncated"
}

// collectCompressionEvents scans steps and returns events for all steps that
// have been compressed (i.e. have a non-empty CompressedObservation).
func collectCompressionEvents(steps []NBAgentPlannerToolActionStep) []compressionEvent {
	var events []compressionEvent
	for _, step := range steps {
		if step.CompressedObservation == "" {
			continue
		}
		method := "truncated"
		if strings.HasPrefix(step.CompressedObservation, "[summarized from") {
			method = "llm_summary"
		}
		events = append(events, compressionEvent{
			toolID:        step.Action.ToolID,
			toolName:      step.Action.Tool,
			originalLen:   len(step.Observation),
			compressedLen: len(step.CompressedObservation),
			method:        method,
		})
	}
	return events
}

// formatCompressionSummary builds a human-readable summary of compression events.
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
		fmt.Fprintf(&sb, "- Step %s (%s): %d → %d chars (%s)\n",
			id, e.toolName, e.originalLen, e.compressedLen, e.method)
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

	events := collectCompressionEvents(steps)
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

	// Build a concise title for the UI card.
	llmCount := 0
	truncCount := 0
	for _, e := range events {
		if e.method == "llm_summary" {
			llmCount++
		} else {
			truncCount++
		}
	}
	title := fmt.Sprintf("Compressed %d observations to fit context window", len(events))

	err := GetConversationDao().SaveConversationToolCall(
		request.ConversationId,
		request.AccountId,
		request.UserId,
		request.MessageId,
		agentID,
		toolID,
		compressionVisibilityToolName,
		title,
		"Compressing older observations to preserve context quality within token budget",
		"", // sqlArgs
		summary,
		toolcore.NBToolResponseStatusSuccess,
		toolcore.NBToolTypeTool,
		nil,
		nil,
	)
	if err != nil {
		ctx.GetLogger().Warn("scratchpad: failed to save compression visibility", "error", err)
	}
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
