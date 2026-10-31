package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"nudgebee/llm/common"
	toolcore "nudgebee/llm/tools/core"
)

// This file holds symbols shared across ReAct-family planners. They originally
// lived in planner_react_2.go alongside the (now-deleted) NBReActPlanner2; they
// were extracted here when that legacy planner was removed so the ReAct3 planner
// (planner_react_3.go) and agents that implement these interfaces keep compiling.

func generateToolId(tool string, input string) string {
	// Normalize input for hashing: remove whitespace and use lower case
	normalized := strings.ToLower(strings.TrimSpace(input))
	hash := common.HashString(normalized)
	// Return a deterministic but unique-ish ID (first 8 chars of hash)
	return fmt.Sprintf("%s-%s", strings.ToLower(tool), hash[:8])
}

// resolveToolInputMacros expands `[[Time:...]]` macros in a tool input before the
// action is dispatched.
//
// It exists as a shared seam, rather than a call in each planner, because being
// ReAct3-only is exactly what caused the incident it fixes (#39201). ReAct3 expanded
// on every tool input; ReAct4 did not, and ReAct4 is the default engine
// (llm_server_planner_engine defaults "auto", and every provider we ship except
// sagemaker/huggingface routes to it). So nothing expanded them: `[[Time:-24h]]`
// reached tools verbatim, and in an Elasticsearch range filter it matches nothing
// while still returning HTTP 200 — an empty result the caller reads as "no data in
// this environment" rather than as a malformed query.
//
// The macro syntax is taught by a shared prompt fragment
// (prompts/default/v1/_fragments/time_handling_rules.yaml) injected into many agents'
// prompts, so any agent can emit one into any tool. That breadth is why this belongs
// to the planners rather than to individual tools, and why a third planner must
// inherit it by construction instead of by someone remembering.
//
// Callers must apply this to the execution-facing input ONLY.
// NBAgentPlannerToolAction.NativeToolInput has to keep the provider's original bytes
// for exact history replay (renderStepsToMessages); rewriting it would replay
// arguments the model never emitted and can invalidate provider thought signatures.
// Callers must also derive any dedup id from the PRE-expansion input, or a fresh
// timestamp makes every repeat unique and silently disables dedup.
func resolveToolInputMacros(input string) string {
	return common.SubstituteDateMacros(input)
}

const metricNoDataTag = "[METRIC-NO-DATA]"

// shortcut: only known metrics query names and response shapes are recognized; extend when a new metrics tool's output contract is reviewed.
func metricQueryReturnedNoData(toolName, response string) bool {
	response = strings.TrimSpace(response)
	switch toolName {
	case "prometheus_execute":
		return response == "" || response == plannerToolNoData || strings.HasPrefix(strings.ToLower(response), "no data found for query:")
	case "datadog_metrics_execute":
		if response == "" || response == plannerToolNoData {
			return true
		}
		var payload struct {
			Series []json.RawMessage `json:"series"`
		}
		return json.Unmarshal([]byte(response), &payload) == nil && payload.Series != nil && len(payload.Series) == 0
	case "es_metrics_query", "splunk_metrics_query":
		if response == "" || response == plannerToolNoData {
			return true
		}
		var envelope map[string]json.RawMessage
		if json.Unmarshal([]byte(response), &envelope) != nil {
			return false
		}
		rawResults, ok := envelope["results"]
		if !ok {
			return false
		}
		var results []json.RawMessage
		if json.Unmarshal(rawResults, &results) != nil || results == nil {
			return false
		}
		for _, rawResult := range results {
			var result map[string]json.RawMessage
			if json.Unmarshal(rawResult, &result) != nil {
				return false
			}
			var totalSeries int
			if json.Unmarshal(result["total_series"], &totalSeries) != nil || totalSeries != 0 {
				return false
			}
			rawPayload, ok := result["payload"]
			if !ok {
				return false
			}
			var series []json.RawMessage
			if json.Unmarshal(rawPayload, &series) != nil || series == nil || len(series) != 0 {
				return false
			}
		}
		return true
	case "openobserve_metrics_execute":
		return response == "" || response == plannerToolNoData || response == "[]"
	default:
		return false
	}
}

func metricNoDataMarker(step NBAgentPlannerToolActionStep) string {
	if step.Status == ToolStatusFailure || step.Status == ToolStatusWaiting || step.Status == ToolStatusWaitingForClient {
		return ""
	}
	if !metricQueryReturnedNoData(step.Action.Tool, step.Observation) {
		return ""
	}
	ref := step.Action.DisplayID
	if ref == "" {
		ref = step.Action.ToolID
	}
	if ref != "" {
		ref = " (step " + ref + ")"
	}
	return fmt.Sprintf("%s %s%s returned no metric series. This is unavailable, not a measured zero, and supports no numeric metric value.", metricNoDataTag, step.Action.Tool, ref)
}

func withMetricNoDataMarker(step NBAgentPlannerToolActionStep, response string) string {
	marker := metricNoDataMarker(step)
	if marker == "" || strings.Contains(response, metricNoDataTag) {
		return response
	}
	return marker + "\n" + response
}

func metricNoDataSummary(steps []NBAgentPlannerToolActionStep) string {
	var b strings.Builder
	for _, step := range steps {
		if marker := metricNoDataMarker(step); marker != "" {
			b.WriteString("- ")
			b.WriteString(marker)
			b.WriteByte('\n')
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "<metric_data_availability>\n" + b.String() +
		"Do not turn an empty metric result into zero or infer a value from another metric. " +
		"Any measured metric number must be supported by a non-empty metric result or an explicit calculation from its returned samples; cite the supporting step. " +
		"If the relevant reads returned no data, report the metric as unavailable and cite these empty results.\n</metric_data_availability>"
}

type NBAgentReActPlannerCritiqueSupport interface {
	CritiqueEnabled() bool
}

type NBAgentReActPlannerSummaryToolProvider interface {
	GetSummaryToolName() string
}

// RetryConfig holds the configuration for the retry mechanism.
type RetryConfig struct {
	MaxRetries int
	Prompts    []string
}

// ErrParseFailure indicates the LLM response could not be parsed into a
// valid action or final answer and is eligible for retrying with a
// reformat prompt.
var ErrParseFailure = errors.New("unable to parse LLM response: no action, final answer, or clarification")

// reactCritiquerInputVariables is shared by every ReAct planner because they
// render the same react_critiquer prompt. Keeping one declaration prevents a
// new runtime input from silently disabling critique in only one planner.
var reactCritiquerInputVariables = []string{
	"input", "scratchpad", "final_answer", "question_type", "tool_names",
	"tool_descriptions", "tools_invoked", "hypothesis_mode_enabled",
	"sdg_grounding_enabled", "premise_verification_enabled", "notebook", "today",
}

// ErrNotebookOnlyTurn signals that the LLM output was a VALID notebook update
// (the model "remembering" a finding) with no accompanying tool action or final
// answer. It is deliberately distinct from ErrParseFailure: the turn is not
// malformed, so the caller must NOT run the generic reformat-retry loop (which
// mislabels the notebook as malformed and burns MaxRetries). Instead it issues a
// single targeted nudge to continue, bounded by maxConsecutiveNotebookOnlyTurns.
var ErrNotebookOnlyTurn = errors.New("planner: turn updated the notebook but emitted no action or final answer")

// reActMemoryRefRegex extracts each <ref n="N" note="..."/> from a
// <memory_used> child of an <action>. Uses raw regex (rather than XML)
// because the surrounding action XML is already lenient in this parser
// and we want a defensive/independent extractor — a malformed n="foo"
// drops just that ref, not the whole action. Attribute order tolerant:
// the note attribute is captured separately via a second regex if the
// n-only pattern doesn't include it.
//
// Attr class is `(?:[^'">]|"[^"]*"|'[^']*')*?` so a `/` OR `>` inside
// a quoted note value — e.g. `note="path/to/resource"` or
// `note="value > 5"` — doesn't prematurely close the attribute run and
// drop the whole ref. The trailing `(?:/\s*)?` still tolerates the
// self-closing form. n= and note= regexes accept both double- and
// single-quoted values because small models occasionally emit `n='1'`
// even though the prompt shows `n="1"`.
var reActMemoryRefRegex = regexp.MustCompile(`(?s)<ref\b((?:[^'">]|"[^"]*"|'[^']*')*?)(?:/\s*)?>`)
var reActMemoryRefNAttrRegex = regexp.MustCompile(`\bn\s*=\s*(?:"(\d+)"|'(\d+)')`)
var reActMemoryRefNoteAttrRegex = regexp.MustCompile(`(?s)\bnote\s*=\s*(?:"([^"]*)"|'([^']*)')`)
var reActMemoryUsedBlockRegex = regexp.MustCompile(`(?s)<memory_used\b[^>]*>(.*?)</memory_used>`)

// parseMemoryUsedFromActionContent extracts the LLM's per-action memory
// attribution from an <action> element's inner text. Returns nil when no
// <memory_used> block is present (the common case — most actions won't
// have one). Malformed refs (missing n, non-numeric n, unknown position)
// are dropped individually and logged at Debug; the action itself is
// never failed on attribution problems — attribution is best-effort audit
// signal, not gating logic.
//
// Deduplicates by position within the same action so a repeated
// <ref n="3"/> doesn't inflate the used-count downstream.
func parseMemoryUsedFromActionContent(actionContent string) []NBAgentPlannerToolActionMemoryRef {
	if actionContent == "" {
		return nil
	}
	blockMatch := reActMemoryUsedBlockRegex.FindStringSubmatch(actionContent)
	if len(blockMatch) < 2 {
		return nil
	}
	inner := blockMatch[1]
	if strings.TrimSpace(inner) == "" {
		return nil
	}
	refMatches := reActMemoryRefRegex.FindAllStringSubmatch(inner, -1)
	if len(refMatches) == 0 {
		return nil
	}
	seen := make(map[int]struct{}, len(refMatches))
	out := make([]NBAgentPlannerToolActionMemoryRef, 0, len(refMatches))
	for _, m := range refMatches {
		attrs := m[1]
		nAttr := reActMemoryRefNAttrRegex.FindStringSubmatch(attrs)
		// n= regex has TWO capture groups (double-quoted vs single-quoted);
		// exactly one non-empty per successful match. Whichever fired is n.
		if len(nAttr) < 3 {
			slog.Debug("planner.memory_used: ref missing n= attribute, skipping", "attrs", attrs)
			continue
		}
		nStr := nAttr[1]
		if nStr == "" {
			nStr = nAttr[2]
		}
		pos, err := strconv.Atoi(nStr)
		if err != nil || pos <= 0 {
			slog.Debug("planner.memory_used: ref n= not a positive int, skipping", "n", nStr)
			continue
		}
		if _, dup := seen[pos]; dup {
			continue
		}
		seen[pos] = struct{}{}
		var note string
		// note= regex mirrors the n= dual-quote pattern.
		if noteAttr := reActMemoryRefNoteAttrRegex.FindStringSubmatch(attrs); len(noteAttr) >= 3 {
			note = noteAttr[1]
			if note == "" {
				note = noteAttr[2]
			}
			note = strings.TrimSpace(note)
		}
		out = append(out, NBAgentPlannerToolActionMemoryRef{Position: pos, Note: note})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func reActPromptToolNames(tools []toolcore.NBTool) string {
	var tn strings.Builder
	for i, tool := range tools {
		if i > 0 {
			tn.WriteString(", ")
		}
		tn.WriteString(tool.Name())
	}

	return tn.String()
}
