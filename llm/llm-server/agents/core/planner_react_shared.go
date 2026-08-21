package core

import (
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
