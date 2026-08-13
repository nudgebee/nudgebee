package tools

import (
	"fmt"
	"strings"

	"nudgebee/code-analysis-agent/tools/core"
)

// Shared result formatting for the search tools (rg, grep). The goals:
//   - Reference-first: a broad result returns a per-file summary that pushes the
//     agent to narrow and open a file, instead of dumping (and inviting citation
//     of) hundreds of raw lines.
//   - Honest truncation: when a result is capped, say so and say how to narrow.
//   - No-match guidance: steer toward a strategy change, not a case/glob re-run.
//
// The user-facing strings below teach the general class of failure (broad
// substring searches over-match; empty searches need a strategy change), not any
// one repo or incident.

// referenceThreshold is the number of match lines above which a content search
// switches to a per-file reference summary instead of dumping every line.
const referenceThreshold = 20

// maxFilesListed caps how many files a reference summary enumerates.
const maxFilesListed = 50

type rgMatch struct{ file, line, content string }

// noMatchObservation guides the agent to change strategy instead of re-running
// near-identical empty searches.
const noMatchObservation = "No matches found. Broaden the pattern or drop a filter (type/path/glob); if you expected a hit, the symbol may live in a test/vendor/generated file — retry with include_tests:true. Do NOT re-run the same search with only a case or glob tweak."

func truncationHint(maxResults int) string {
	return fmt.Sprintf("IS_TRUNCATED: true — capped at %d. The pattern is broad and is likely matching more than you intend (e.g. a substring of a longer identifier). Narrow with a `path`/`type` filter or a more specific/anchored pattern, or set files_only:true, then file_view the winner before citing.\n", maxResults)
}

func relToWorkspace(p, workspaceDir string) string {
	if workspaceDir == "" {
		return p
	}
	if p == workspaceDir {
		return "."
	}
	// Match on a trailing-slash boundary so "/w/clone-12" doesn't match a path
	// under a sibling "/w/clone-123/...".
	dir := workspaceDir
	if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	if strings.HasPrefix(p, dir) {
		return strings.TrimPrefix(p, dir)
	}
	return p
}

func noMatchResponse(extra map[string]any) core.NBToolResponse {
	data := map[string]any{"matches": []map[string]any{}, "total_count": 0, "truncated": false}
	for k, v := range extra {
		data[k] = v
	}
	return core.NBToolResponse{Status: "success", Observation: noMatchObservation, Data: data}
}

// renderFilesList formats a files-with-matches result.
func renderFilesList(files []string, maxResults int, extra map[string]any) core.NBToolResponse {
	truncated := len(files) >= maxResults
	var obs strings.Builder
	fmt.Fprintf(&obs, "Found %d file(s) with matches:\n\n", len(files))
	dataMatches := make([]map[string]any, 0, len(files))
	for i, f := range files {
		fmt.Fprintf(&obs, "%d. %s\n", i+1, f)
		dataMatches = append(dataMatches, map[string]any{"file": f})
	}
	if truncated {
		obs.WriteString("\n" + truncationHint(maxResults))
	}
	data := map[string]any{"matches": dataMatches, "total_count": len(files), "truncated": truncated}
	for k, v := range extra {
		data[k] = v
	}
	return core.NBToolResponse{Status: "success", Observation: obs.String(), Data: data}
}

// renderContentMatches decides between a detailed line view (focused result) and
// a per-file reference summary (broad result), and appends an honest truncation
// signal when capped.
func renderContentMatches(matches []rgMatch, order []string, byFile map[string][]rgMatch, maxResults int, extra map[string]any) core.NBToolResponse {
	if len(matches) == 0 {
		return noMatchResponse(extra)
	}

	truncated := len(matches) >= maxResults

	if len(matches) > referenceThreshold || len(order) > 8 {
		return renderReference(order, byFile, len(matches), truncated, maxResults, extra)
	}

	var obs strings.Builder
	fmt.Fprintf(&obs, "Found %d match(es):\n\n", len(matches))
	dataMatches := make([]map[string]any, 0, len(matches))
	for i, m := range matches {
		if m.line != "" {
			fmt.Fprintf(&obs, "%d. **%s:%s**\n   `%s`\n\n", i+1, m.file, m.line, m.content)
		} else {
			fmt.Fprintf(&obs, "%d. **%s**\n   `%s`\n\n", i+1, m.file, m.content)
		}
		dm := map[string]any{"file": m.file, "content": m.content}
		if m.line != "" {
			dm["line"] = m.line
		}
		dataMatches = append(dataMatches, dm)
	}
	if truncated {
		obs.WriteString(truncationHint(maxResults))
	}
	data := map[string]any{"matches": dataMatches, "total_count": len(matches), "truncated": truncated}
	for k, v := range extra {
		data[k] = v
	}
	return core.NBToolResponse{Status: "success", Observation: obs.String(), Data: data}
}

func renderReference(order []string, byFile map[string][]rgMatch, total int, truncated bool, maxResults int, extra map[string]any) core.NBToolResponse {
	var obs strings.Builder
	fmt.Fprintf(&obs, "Found %d match(es) across %d file(s) — per-file summary (reference-first). Open the most relevant file with file_view before citing anything.\n\n", total, len(order))
	dataMatches := make([]map[string]any, 0, len(order))
	for i, f := range order {
		if i >= maxFilesListed {
			fmt.Fprintf(&obs, "… and %d more file(s).\n", len(order)-maxFilesListed)
			break
		}
		ms := byFile[f]
		lineNums := make([]string, 0, 12)
		for _, m := range ms {
			if m.line == "" || len(lineNums) >= 12 {
				continue
			}
			lineNums = append(lineNums, m.line)
		}
		more := ""
		if len(ms) > len(lineNums) {
			more = ", …"
		}
		fmt.Fprintf(&obs, "File: %s — %d match(es) @ lines %s%s\n", f, len(ms), strings.Join(lineNums, ", "), more)
		dataMatches = append(dataMatches, map[string]any{"file": f, "count": len(ms)})
	}
	if truncated {
		obs.WriteString("\n" + truncationHint(maxResults))
	} else {
		obs.WriteString("\nToo broad to cite directly — narrow with a `path`/`type` filter or a more specific pattern, then file_view the winner.\n")
	}
	data := map[string]any{"matches": dataMatches, "total_count": total, "truncated": truncated, "reference_mode": true}
	for k, v := range extra {
		data[k] = v
	}
	return core.NBToolResponse{Status: "success", Observation: obs.String(), Data: data}
}

// parseMatchLines turns raw grep/rg "file:line:content" (or "file:content")
// output into structured matches, skipping context/`--` separator lines.
func parseMatchLines(lines []string, workspaceDir string) (matches []rgMatch, order []string, byFile map[string][]rgMatch) {
	byFile = map[string][]rgMatch{}
	for _, line := range lines {
		if line == "" || line == "--" {
			continue
		}
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 2 {
			continue
		}
		var m rgMatch
		m.file = relToWorkspace(parts[0], workspaceDir)
		// Only treat the middle field as a line number when it actually is one.
		// With line numbers disabled the format is "file:content", and content
		// can itself contain colons — guarding avoids parsing content as a line.
		if len(parts) == 3 && isAllDigits(strings.TrimSpace(parts[1])) {
			m.line = strings.TrimSpace(parts[1])
			m.content = strings.TrimSpace(parts[2])
		} else {
			m.content = strings.TrimSpace(line[len(parts[0])+1:])
		}
		if _, seen := byFile[m.file]; !seen {
			order = append(order, m.file)
		}
		byFile[m.file] = append(byFile[m.file], m)
		matches = append(matches, m)
	}
	return matches, order, byFile
}
