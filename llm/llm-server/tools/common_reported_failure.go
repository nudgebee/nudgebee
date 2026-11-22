package tools

import (
	"encoding/json"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/tools/core"
)

const (
	reportedFailureModeOff = "off"
	reportedFailureModeLog = "log"

	// maxLoggedStatusLen bounds the one body value the shape log keeps, so only
	// a short enum-like status can ever reach the logs.
	maxLoggedStatusLen = 32
)

// withReportedFailure lets a tool judge its own OK reply by the fields it
// declared. In log mode it only counts a "would fail"; the status changes only
// for tools listed in LlmServerToolReportedFailureEnforceTools.
func withReportedFailure(nbCtx core.NbToolContext, toolName string, resp core.NBToolResponse, rules core.FailureRules) core.NBToolResponse {
	if reportedFailureDisabled() {
		return resp
	}
	if resp.Status != core.NBToolResponseStatusSuccess {
		return resp
	}
	failed, reason := core.ClassifyResponse(resp.Data, rules)
	if !failed {
		return resp
	}

	outcome := "would_fail"
	enforced := toolInEnforceList(toolName)
	if enforced {
		outcome = "enforced"
	}
	common.MetricsToolReportedFailure(toolName, string(reason), outcome, nbCtx.AccountId)
	if nbCtx.Ctx != nil {
		nbCtx.Ctx.GetLogger().Warn("tool: reply says it failed", "tool", toolName, "reason", string(reason), "outcome", outcome)
	}
	if !enforced {
		return resp
	}

	resp.Status = core.NBToolResponseStatusError
	// Copied, not mutated: the tool may hold the same pointer (a cached reply).
	meta := core.NBToolResponseMetadata{}
	if resp.Metadata != nil {
		meta = *resp.Metadata
	}
	meta.FailureReason = reason
	resp.Metadata = &meta
	return resp
}

var warnUnknownModeOnce sync.Once

// reportedFailureDisabled is true only for an explicit "off". Any other value
// behaves as "log", and a value that is neither says so once, because a typo
// like "enforce" would otherwise look like the fix did nothing.
func reportedFailureDisabled() bool {
	mode := strings.ToLower(strings.TrimSpace(config.Config.LlmServerToolReportedFailureMode))
	switch mode {
	case reportedFailureModeOff:
		return true
	case reportedFailureModeLog, "":
		return false
	}
	warnUnknownModeOnce.Do(func() {
		slog.Warn("tool: unknown llm_server_tool_reported_failure_mode, treating it as log; use llm_server_tool_reported_failure_enforce_tools to enforce", "mode", mode)
	})
	return false
}

func toolInEnforceList(toolName string) bool {
	raw := config.Config.LlmServerToolReportedFailureEnforceTools
	// An empty list splits to [""], which would match an empty tool name.
	if strings.TrimSpace(raw) == "" || toolName == "" {
		return false
	}
	names := strings.Split(raw, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	return slices.Contains(names, toolName)
}

// maxShapeBodyBytes bounds the reply the shape log will parse: parsing costs
// about 10 ms per MiB, so a larger reply logs only its size.
const maxShapeBodyBytes = 256 * 1024

// logResponseShape logs a reply's shape for the baseline of real api-server
// replies, and never any body text.
func logResponseShape(logger *slog.Logger, errPrefix, actionName string, body []byte) {
	if len(body) > maxShapeBodyBytes {
		logger.Info(errPrefix+": rpc response shape", "action", actionName, "size_bytes", len(body), "shape", "skipped: body too large")
		return
	}
	keys, status := responseShape(body)
	logger.Info(errPrefix+": rpc response shape", "action", actionName, "keys", keys, "status", status)
}

// responseShape is the only part of an RPC body that is logged: the sorted
// top-level key names and, when it is a short string, the status value. Body
// text can carry tenant data and command output, so none of it is kept.
func responseShape(body []byte) (keys []string, status string) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, ""
	}
	keys = make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if raw, ok := obj["status"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil && len(s) <= maxLoggedStatusLen {
			status = s
		}
	}
	return keys, status
}
