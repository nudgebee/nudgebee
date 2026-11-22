package core

import (
	"fmt"
	"strconv"
	"strings"

	"nudgebee/llm/config"
	toolcore "nudgebee/llm/tools/core"
)

// analyzeMetricSeriesToolName is the tool that takes a saved metric series file
// as its file_ref input. The note below names it only when the planner has it.
const analyzeMetricSeriesToolName = "analyze_metric_series"

// savedFilesNoteMaxNames caps how many file names one step's note lists; the
// rest are counted as "+N more". The note rides every re-render of the step.
const savedFilesNoteMaxNames = 5

// savedFilesNote returns a short line naming the workspace files a step saved
// (its Type:"file" references, including those a sub-agent's tools saved and
// passed up), or "" when there is nothing new to show. Without it the planner
// learns a saved file's name only if the tool or sub-agent prose repeats it,
// and the names (e.g. metrics_prometheus_<unixnano>.json) can't be guessed.
// ReAct4 only: ReAct3's formatObservation already lists every reference.
//
// rendered is the observation text the model sees for the step; a file whose
// name already appears in it is skipped. Names are listed in reference order,
// deduplicated. Knowledge documents (Query "knowledge:...") are read through
// load_skills, so they get their own line. The hints name only tools the
// planner has. Render-only: the step is not modified, and the output depends
// only on the step, rendered and the planner's tool set, so a re-rendered
// step's text does not change between iterations.
func savedFilesNote(step *NBAgentPlannerToolActionStep, rendered string, tools []toolcore.NBTool) string {
	if step == nil || len(step.References) == 0 || !config.Config.LlmServerFsEvidenceRecallEnabled {
		return ""
	}
	var files, docs []string
	seen := make(map[string]bool, len(step.References))
	for _, ref := range step.References {
		if ref.Type != "file" || ref.Url == "" || seen[ref.Url] {
			continue
		}
		seen[ref.Url] = true
		if strings.Contains(rendered, ref.Url) {
			continue
		}
		if strings.HasPrefix(ref.Query, "knowledge:") {
			docs = append(docs, fmt.Sprintf("%q (skill_name=%s)", ref.Url, ref.Query))
			continue
		}
		files = append(files, strconv.Quote(ref.Url))
	}

	var b strings.Builder
	if len(files) > 0 {
		b.WriteString("\n[saved workspace files: ")
		b.WriteString(joinCappedNames(files))
		var hints []string
		if plannerHasTool(tools, toolcore.ToolExecuteShellCommand) {
			hints = append(hints, "read with shell_execute (grep/head)")
		}
		if plannerHasTool(tools, analyzeMetricSeriesToolName) {
			hints = append(hints, "a metric series file can be passed to analyze_metric_series as file_ref")
		}
		if len(hints) > 0 {
			b.WriteString(" — ")
			b.WriteString(strings.Join(hints, "; "))
		}
		b.WriteString("]")
	}
	if len(docs) > 0 {
		b.WriteString("\n[reference documentation saved: ")
		b.WriteString(joinCappedNames(docs))
		b.WriteString(" — read with load_skills using that skill_name with keyword or start_line; if a handle expired, search again]")
	}
	return b.String()
}

// joinCappedNames joins at most savedFilesNoteMaxNames names and counts the rest.
func joinCappedNames(names []string) string {
	if len(names) <= savedFilesNoteMaxNames {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s +%d more", strings.Join(names[:savedFilesNoteMaxNames], ", "), len(names)-savedFilesNoteMaxNames)
}

// plannerHasTool reports whether the planner's tool set has a tool by this name
// (case-insensitive, aliases included).
func plannerHasTool(tools []toolcore.NBTool, name string) bool {
	for _, t := range tools {
		if t == nil {
			continue
		}
		if strings.EqualFold(t.Name(), name) {
			return true
		}
		if aliased, ok := t.(interface{ GetNameAliases() []string }); ok {
			for _, alias := range aliased.GetNameAliases() {
				if strings.EqualFold(alias, name) {
					return true
				}
			}
		}
	}
	return false
}
