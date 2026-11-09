package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"nudgebee/code-analysis-agent/tools/core"
)

// A search for a name that finds nothing leaves the agent guessing: is the name
// spelled differently, hidden by one of its own filters, or not in the
// repository at all? It answered by trying one variant after another, and
// sometimes by describing nearby code as if the name existed. noMatchVerdict
// settles the question in the same call: it looks through the whole repository
// itself and says where the name is, which part of it is, or that it is nowhere.

const (
	// absenceCheckTimeout bounds all the extra searches of one verdict
	// together. When they do not finish the verdict is withheld: absence is
	// only ever stated after searches that ran to the end.
	absenceCheckTimeout = 20 * time.Second
	// absenceFilesListed caps the files named when the name is found.
	absenceFilesListed = 12
	// absencePartsTried caps how many parts of a name are looked up.
	absencePartsTried = 3
	// absenceLinesRead caps how much of a search's output is read; the search
	// is stopped there. Enough to fill the file list, small enough that a name
	// matching half the repository costs nothing.
	absenceLinesRead = 400
)

// isPlainName reports whether a search pattern is a name rather than a regular
// expression. Only a name can be declared absent: for an expression, "no match"
// says nothing about what exists.
func isPlainName(pattern string) bool {
	letters := 0
	for _, r := range pattern {
		switch {
		case isNameRune(r):
			letters++
		case r != '_' && r != '-' && r != '.':
			return false
		}
	}
	return len(pattern) >= 4 && letters >= 3
}

func isNameRune(r rune) bool {
	return (r >= '0' && r <= '9') || isLower(r) || isUpper(r)
}

func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }
func isLower(r rune) bool { return r >= 'a' && r <= 'z' }

// nameWords splits a name into its words: at separators, where a lower-case
// letter is followed by an upper-case one (toolBudget), and where a run of
// capitals ends in a capitalised word (URLPath is URL, Path).
func nameWords(name string) []string {
	runes := []rune(name)
	var words []string
	var cur []rune
	flush := func() {
		if len(cur) > 0 {
			words = append(words, string(cur))
			cur = nil
		}
	}
	for i, r := range runes {
		if r == '_' || r == '-' || r == '.' {
			flush()
			continue
		}
		if isUpper(r) && len(cur) > 0 {
			prev := cur[len(cur)-1]
			nextLower := i+1 < len(runes) && isLower(runes[i+1])
			if isLower(prev) || (isUpper(prev) && nextLower) {
				flush()
			}
		}
		cur = append(cur, r)
	}
	flush()
	return words
}

// looseNamePattern is a regular expression matching a name however its words
// are joined: ToolBudget, tool_budget, tool-budget, toolbudget. It is used
// case-insensitively. Empty when the name has no words.
func looseNamePattern(name string) string {
	words := nameWords(name)
	for i, w := range words {
		words[i] = regexp.QuoteMeta(w)
	}
	return strings.Join(words, `[_\-.]?`)
}

// nameParts lists the parts of a name worth looking up when the whole name is
// nowhere: a name is often put together at run time from a fixed part and a
// prefix or suffix ("cron-" + job name). A part qualifies only if it is itself
// specific — several words, or long — so that finding it means something.
func nameParts(name string) []string {
	specific := func(s string) bool {
		return s != name && (len(nameWords(s)) >= 2 && len(s) >= 8 || len(s) >= 12)
	}
	seen := map[string]bool{}
	var parts []string
	add := func(s string) {
		if specific(s) && !seen[s] {
			seen[s] = true
			parts = append(parts, s)
		}
	}
	// Pieces between the stronger separators.
	for _, piece := range strings.FieldsFunc(name, func(r rune) bool { return r == '-' || r == '.' }) {
		add(piece)
	}
	// A snake_case name without its first, or its last, word.
	if words := strings.Split(name, "_"); len(words) >= 3 && !strings.ContainsAny(name, "-.") {
		add(strings.Join(words[1:], "_"))
		add(strings.Join(words[:len(words)-1], "_"))
	}
	sort.SliceStable(parts, func(i, j int) bool { return len(parts[i]) > len(parts[j]) })
	if len(parts) > absencePartsTried {
		parts = parts[:absencePartsTried]
	}
	return parts
}

// rgLines runs ripgrep and returns the output lines that keep accepts, at most
// limit of them; the search is stopped once it has that many. completed is
// false when the search could not be run to the end for any other reason, in
// which case nothing may be concluded from what it returned.
func (t *RipgrepTool) rgLines(ctx context.Context, limit int, keep func(string) bool, args ...string) (lines []string, completed bool) {
	cmd := exec.CommandContext(ctx, "rg", args...)
	cmd.Dir = t.workspaceDir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false
	}
	if err := cmd.Start(); err != nil {
		return nil, false
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	full := false
	for scanner.Scan() {
		if line := scanner.Text(); keep == nil || keep(line) {
			lines = append(lines, line)
			if len(lines) >= limit {
				full = true
				break
			}
		}
	}
	scanErr := scanner.Err()
	if full || scanErr != nil {
		// Stop the search rather than leave it blocked writing output nobody reads.
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	switch {
	case full:
		return lines, true
	case scanErr != nil || ctx.Err() != nil:
		return nil, false
	case waitErr == nil:
		return lines, true
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
		return lines, true // ran to the end, nothing to report
	}
	return nil, false
}

// nameFound is where a name was found: in the contents of files, as the name
// of files, or both.
type nameFound struct {
	order  []string             // files whose contents hold the name
	byFile map[string][]rgMatch // their matching lines
	named  []string             // files named like it
}

func (f nameFound) any() bool { return len(f.order) > 0 || len(f.named) > 0 }

// findName looks for a name in every file of the repository that ripgrep reads:
// contents and file names, no path, type or glob filter, hidden files, tests
// and vendored code included, any letter case, any joining of its words.
func (t *RipgrepTool) findName(ctx context.Context, name, repoDir string) (found nameFound, completed bool) {
	loose := looseNamePattern(name)
	nameRe, err := regexp.Compile("(?i)" + loose)
	if loose == "" || err != nil {
		return found, false
	}

	lines, ok := t.rgLines(ctx, absenceLinesRead, nil,
		"--line-number", "--no-heading", "--color=never", "--ignore-case", "--max-count=5", "--max-columns=300",
		"--hidden", "--glob=!.git", "--", loose, repoDir)
	if !ok {
		return found, false
	}
	_, found.order, found.byFile = parseMatchLines(lines, t.workspaceDir)

	named, ok := t.rgLines(ctx, absenceFilesListed+1, func(path string) bool {
		return nameRe.MatchString(filepath.Base(path))
	}, "--files", "--hidden", "--glob=!.git", repoDir)
	if !ok {
		return found, false
	}
	for _, path := range named {
		found.named = append(found.named, relToWorkspace(path, t.workspaceDir))
	}
	return found, true
}

// write renders where the name was found: "File: path — n match(es) @ lines …"
// for contents, the same shape a broad search result uses, then file names.
func (f nameFound) write(obs *strings.Builder) {
	for i, file := range f.order {
		if i >= absenceFilesListed {
			obs.WriteString("… and more file(s).\n")
			break
		}
		nums := make([]string, 0, 6)
		for _, m := range f.byFile[file] {
			if m.line != "" && len(nums) < 6 {
				nums = append(nums, m.line)
			}
		}
		fmt.Fprintf(obs, "File: %s — %d match(es) @ lines %s\n", file, len(f.byFile[file]), strings.Join(nums, ", "))
	}
	for i, file := range f.named {
		if i >= absenceFilesListed {
			obs.WriteString("… and more file(s) named like it.\n")
			break
		}
		fmt.Fprintf(obs, "File named like it: %s\n", file)
	}
}

// noMatchVerdict is the result of a content search that matched nothing.
func (t *RipgrepTool) noMatchVerdict(ctx context.Context, pattern, repoDir string) core.NBToolResponse {
	if !isPlainName(pattern) {
		return noMatchResponse(nil)
	}
	ctx, cancel := context.WithTimeout(ctx, absenceCheckTimeout)
	defer cancel()

	found, completed := t.findName(ctx, pattern, repoDir)
	if !completed {
		return noMatchResponse(nil)
	}
	if found.any() {
		var obs strings.Builder
		fmt.Fprintf(&obs, "No match for `%s` as you searched for it, but it is in the repository. Looking at the contents and names of every file — hidden files, tests and vendored files included, any letter case, its words joined in any way — finds this text, as a name or inside a longer one:\n", pattern)
		found.write(&obs)
		obs.WriteString("Search again with one of these paths (and include_tests:true for a test file), or open the file, to see the lines.\n")
		return core.NBToolResponse{Status: "success", Observation: obs.String(), Data: map[string]any{
			"matches": []map[string]any{}, "total_count": 0, "truncated": false, "found_elsewhere": true,
		}}
	}

	parts := nameParts(pattern)
	for _, part := range parts {
		found, completed := t.findName(ctx, part, repoDir)
		if !completed {
			return noMatchResponse(nil)
		}
		if !found.any() {
			continue
		}
		var obs strings.Builder
		fmt.Fprintf(&obs, "No match anywhere in the repository for the full name `%s`, but part of it, `%s`, is here:\n", pattern, part)
		found.write(&obs)
		obs.WriteString("The full name may be put together at run time, with a prefix or suffix added in code. Search for the part to see the lines.\n")
		return core.NBToolResponse{Status: "success", Observation: obs.String(), Data: map[string]any{
			"matches": []map[string]any{}, "total_count": 0, "truncated": false, "part_found": part,
		}}
	}

	var obs strings.Builder
	fmt.Fprintf(&obs, "No match anywhere in this repository for `%s`. Searched: the contents and the names of every file, hidden files, tests and vendored files included, any letter case, its words joined in any way (_ - . or nothing)", pattern)
	if len(parts) > 0 {
		fmt.Fprintf(&obs, ", and its parts `%s`", strings.Join(parts, "`, `"))
	}
	obs.WriteString(". Not searched: files the repository ignores, such as build output and installed dependencies.\n")
	obs.WriteString("Under this name it does not exist here, and other spellings of the same name will not find it. If the question is about it, say that it does not exist in this repository; describe related code only if you have read it.\n")
	return core.NBToolResponse{Status: "success", Observation: obs.String(), Data: map[string]any{
		"matches": []map[string]any{}, "total_count": 0, "truncated": false, "definitive_no_match": true,
	}}
}
