package triage

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/pmezard/go-difflib/difflib"
)

// RecordedChange is what a configuration-change event recorded about one edit.
//
// The agent stores the whole object before and after the edit in the event's
// `json` evidence block (obj / oldObj). Its `diff` block carries a per-path
// summary instead, and caps ConfigMap values at 2048 bytes by keeping the head
// of each side — so an edit further into a large value (a whole JSON or YAML
// file held in one key) is not in that summary at all. Before/After come from
// the json block; Paths is the diff block's summary, read only when the json
// block is missing.
type RecordedChange struct {
	Kind   string // lowercased Kubernetes kind ("configmap", "deployment", ...)
	Before map[string]any
	After  map[string]any
	Paths  []PathChange
}

// PathChange is one entry of a diff block's updated_values.
type PathChange struct {
	Path string
	Old  any
	New  any
}

// ParseRecordedChange reads a configuration-change event's evidences column.
// ok is false when neither the full objects nor a per-path summary is there.
func ParseRecordedChange(evidences json.RawMessage) (RecordedChange, bool) {
	var blocks []struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if len(evidences) == 0 || json.Unmarshal(evidences, &blocks) != nil {
		return RecordedChange{}, false
	}
	var rc RecordedChange
	for _, b := range blocks {
		switch b.Type {
		case "json":
			raw := []byte(b.Data)
			// The agent stores this block's data as a JSON-encoded string.
			var s string
			if json.Unmarshal(raw, &s) == nil {
				raw = []byte(s)
			}
			var payload struct {
				Kind   string         `json:"kind"`
				Obj    map[string]any `json:"obj"`
				OldObj map[string]any `json:"oldObj"`
			}
			if json.Unmarshal(raw, &payload) != nil || payload.Obj == nil || payload.OldObj == nil {
				continue
			}
			rc.Before, rc.After = payload.OldObj, payload.Obj
			if payload.Kind != "" {
				rc.Kind = strings.ToLower(payload.Kind)
			}
		case "diff":
			var d struct {
				ResourceName  string `json:"resource_name"`
				UpdatedValues []struct {
					Path string `json:"path"`
					Old  any    `json:"old"`
					New  any    `json:"new"`
				} `json:"updated_values"`
			}
			if json.Unmarshal(b.Data, &d) != nil {
				continue
			}
			// resource_name is "<kind>/<namespace>/<name>.yaml".
			if rc.Kind == "" && d.ResourceName != "" {
				rc.Kind = strings.ToLower(strings.SplitN(d.ResourceName, "/", 2)[0])
			}
			for _, uv := range d.UpdatedValues {
				rc.Paths = append(rc.Paths, PathChange{Path: uv.Path, Old: uv.Old, New: uv.New})
			}
		}
	}
	return rc, rc.After != nil || len(rc.Paths) > 0
}

const (
	// maxChangeLines and maxChangeItemBytes bound what one change contributes.
	// These lines are inlined into the investigation prompt next to every other
	// candidate, so they name the edit and stop there.
	maxChangeLines     = 5
	maxChangeItemBytes = 300
	maxChangeLineBytes = 160
	// maxAnchorLookback bounds the walk back to the block that encloses a changed
	// line.
	maxAnchorLookback = 50
	// maxMatchedLines bounds the lines, per side, handed to the line matcher
	// once the common head and tail are trimmed. Its cost grows with the cube of
	// the input when lines repeat ("},"), and the stored objects are uncapped, so
	// past this the changed span is described by its first line instead.
	maxMatchedLines = 1000
)

// ChangeLines says what a run of edits to one object changed: the object before
// the earliest edit against the object after the latest one, so repeat edits
// read as their net effect and an edit that was later reverted does not read as
// the change. edits is how many edits the run holds.
func ChangeLines(earliest, latest RecordedChange, edits int) []string {
	if earliest.Before == nil || latest.After == nil {
		return capLines(pathChangeLines(latest.Paths))
	}
	kind := latest.Kind
	if kind == "" {
		kind = earliest.Kind
	}
	lines := objectChangeLines(kind, earliest.Before, latest.After)
	if len(lines) == 0 && edits > 1 {
		return []string{fmt.Sprintf("edited %d times; no net change across those edits", edits)}
	}
	return capLines(lines)
}

// objectChangeLines diffs the fields the agent monitors for each kind: a
// ConfigMap's data / binaryData, a workload's spec, and for an Ingress also its
// annotations (where body-size limits, timeouts and the like live).
func objectChangeLines(kind string, before, after map[string]any) []string {
	var out []string
	if kind == "configmap" {
		for _, field := range []string{"data", "binaryData"} {
			b, _ := before[field].(map[string]any)
			a, _ := after[field].(map[string]any)
			for _, k := range unionSortedKeys(b, a) {
				out = append(out, valueChangeLines(field+"."+k, b[k], a[k])...)
			}
		}
		return out
	}
	walkLeaves("spec", before["spec"], after["spec"], &out)
	if kind == "ingress" {
		walkLeaves("metadata.annotations", annotationsWithoutLastApplied(before), annotationsWithoutLastApplied(after), &out)
	}
	return out
}

// lastAppliedAnnotation is kubectl's copy of the whole object; it changes with
// every `kubectl apply` and repeats the real change.
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

func annotationsWithoutLastApplied(obj map[string]any) map[string]any {
	meta, _ := obj["metadata"].(map[string]any)
	ann, _ := meta["annotations"].(map[string]any)
	out := make(map[string]any, len(ann))
	for k, v := range ann {
		if k != lastAppliedAnnotation {
			out[k] = v
		}
	}
	return out
}

func walkLeaves(path string, before, after any, out *[]string) {
	bm, bIsMap := before.(map[string]any)
	am, aIsMap := after.(map[string]any)
	if bIsMap && aIsMap {
		for _, k := range unionSortedKeys(bm, am) {
			walkLeaves(path+"."+k, bm[k], am[k], out)
		}
		return
	}
	bl, bIsList := before.([]any)
	al, aIsList := after.([]any)
	if bIsList && aIsList {
		for i := 0; i < max(len(bl), len(al)); i++ {
			var b, a any
			if i < len(bl) {
				b = bl[i]
			}
			if i < len(al) {
				a = al[i]
			}
			walkLeaves(fmt.Sprintf("%s[%d]", path, i), b, a, out)
		}
		return
	}
	*out = append(*out, valueChangeLines(path, before, after)...)
}

// valueChangeLines describes one changed value. A multi-line string — a file
// held in a ConfigMap key — is described by the lines that differ, not by its
// whole body.
func valueChangeLines(path string, before, after any) []string {
	if before == nil && after == nil {
		return nil
	}
	if after == nil {
		return []string{path + " removed"}
	}
	if before == nil {
		if s, ok := after.(string); ok && strings.Contains(s, "\n") {
			return []string{fmt.Sprintf("%s added (%d lines)", path, strings.Count(s, "\n")+1)}
		}
		return []string{path + " added: " + formatValue(after)}
	}
	if jsonEqual(before, after) {
		return nil
	}
	bs, bOK := before.(string)
	as, aOK := after.(string)
	if bOK && aOK && (strings.Contains(bs, "\n") || strings.Contains(as, "\n")) {
		return textChangeLines(path, bs, as)
	}
	return []string{path + ": " + formatValue(before) + " → " + formatValue(after)}
}

// textChangeLines lists the changed line ranges of a multi-line value, each
// named by the block it sits in — in a flags file the changed line is
// `"defaultVariant": "25"`, and only the enclosing `"loadGeneratorVUs": {`
// says which flag that is.
func textChangeLines(path, before, after string) []string {
	bl := strings.Split(before, "\n")
	al := strings.Split(after, "\n")
	// Trim the common head and tail first: an edit to a large file is usually a
	// few lines, and the matcher's cost grows with what is left.
	head := 0
	for head < len(bl) && head < len(al) && bl[head] == al[head] {
		head++
	}
	tail := 0
	for tail < len(bl)-head && tail < len(al)-head && bl[len(bl)-1-tail] == al[len(al)-1-tail] {
		tail++
	}
	bMid, aMid := bl[head:len(bl)-tail], al[head:len(al)-tail]
	if len(bMid) > maxMatchedLines || len(aMid) > maxMatchedLines {
		return []string{largeChangeLine(path, bl, head, bMid, aMid)}
	}
	// autoJunk off: it treats lines repeated across the file ("},") as junk,
	// which merges unrelated edits into one replaced range.
	m := difflib.NewMatcherWithJunk(bMid, aMid, false, nil)
	var out []string
	for _, op := range m.GetOpCodes() {
		if op.Tag == 'e' {
			continue
		}
		i1 := head + op.I1
		ref := ""
		if op.J1 < op.J2 {
			ref = aMid[op.J1]
		} else if op.I1 < op.I2 {
			ref = bMid[op.I1]
		}
		where := path
		if anchor := enclosingLine(bl, i1, indentOf(ref)); anchor != "" {
			where += " (in " + anchor + ")"
		}
		removed := joinTrimmed(bMid[op.I1:op.I2])
		added := joinTrimmed(aMid[op.J1:op.J2])
		switch {
		case removed == "" && added == "":
			out = append(out, where+": whitespace changed")
		case removed == "":
			out = append(out, where+": added "+added)
		case added == "":
			out = append(out, where+": removed "+removed)
		default:
			out = append(out, where+": "+removed+" → "+added)
		}
	}
	return out
}

// largeChangeLine names a change too spread out to match line by line: where
// it starts, how far it runs, and its first line on each side.
func largeChangeLine(path string, bl []string, head int, bMid, aMid []string) string {
	ref, first, firstNew := "", "", ""
	if len(bMid) > 0 {
		first = strings.TrimSpace(bMid[0])
		ref = bMid[0]
	}
	if len(aMid) > 0 {
		firstNew = strings.TrimSpace(aMid[0])
		ref = aMid[0]
	}
	where := path
	if anchor := enclosingLine(bl, head, indentOf(ref)); anchor != "" {
		where += " (in " + anchor + ")"
	}
	return fmt.Sprintf("%s: lines %d-%d of %d changed (%d lines now), first: %s → %s",
		where, head+1, head+len(bMid), len(bl), len(aMid), first, firstNew)
}

// enclosingLine finds the nearest line above index i that opens a block and is
// indented less than the changed line — its parent key, not a sibling.
func enclosingLine(lines []string, i, indent int) string {
	for j := min(i, len(lines)) - 1; j >= 0 && j >= i-maxAnchorLookback; j-- {
		t := strings.TrimSpace(lines[j])
		if t == "" || indentOf(lines[j]) >= indent {
			continue
		}
		if strings.HasSuffix(t, "{") || strings.HasSuffix(t, "[") || strings.HasSuffix(t, ":") {
			return t
		}
	}
	return ""
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func joinTrimmed(lines []string) string {
	var parts []string
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			parts = append(parts, t)
		}
	}
	return strings.Join(parts, " | ")
}

// truncationMarker is what the agent appends to a ConfigMap value it capped to
// its head.
var truncationMarker = regexp.MustCompile(`\n\.\.\. \[truncated: \d+ of \d+ bytes shown\]$`)

// excerptHeader heads each range of a value the agent cut to the lines (or
// bytes) that changed. The ranges' line numbers differ between the sides when
// an edit adds lines, so the headers are dropped before the sides are compared.
var excerptHeader = regexp.MustCompile(`(?m)^\.\.\. \[truncated: showing (lines|bytes) \d+-\d+ of \d+\]\n?`)

// pathChangeLines describes a diff block's per-path summary, for events whose
// full objects were not recorded.
func pathChangeLines(paths []PathChange) []string {
	var out []string
	for _, p := range paths {
		bs, bOK := p.Old.(string)
		as, aOK := p.New.(string)
		if bOK && aOK && truncationMarker.MatchString(bs) && truncationMarker.MatchString(as) {
			bHead, aHead := truncationMarker.ReplaceAllString(bs, ""), truncationMarker.ReplaceAllString(as, "")
			if bHead == aHead {
				// Both sides are the same capped head of a longer value: the edit
				// is past what the event kept, and nothing here says what it was.
				out = append(out, p.Path+": changed past the part of the value this event recorded")
				continue
			}
			out = append(out, textChangeLines(p.Path, bHead, aHead)...)
			continue
		}
		if bOK && aOK && (excerptHeader.MatchString(bs) || excerptHeader.MatchString(as)) {
			out = append(out, textChangeLines(p.Path, excerptHeader.ReplaceAllString(bs, ""), excerptHeader.ReplaceAllString(as, ""))...)
			continue
		}
		out = append(out, valueChangeLines(p.Path, p.Old, p.New)...)
	}
	return out
}

// capLines keeps the lines within maxChangeLines / maxChangeItemBytes and says
// how many were left out.
func capLines(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, min(len(lines), maxChangeLines))
	size := 0
	for i, l := range lines {
		l = clip(l, maxChangeLineBytes)
		if len(out) == maxChangeLines || size+len(l) > maxChangeItemBytes {
			out = append(out, fmt.Sprintf("… (+%d more)", len(lines)-i))
			break
		}
		out = append(out, l)
		size += len(l)
	}
	return out
}

func formatValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "(none)"
	case string:
		return clip(strings.ReplaceAll(t, "\n", " "), maxChangeLineBytes/2)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return clip(string(b), maxChangeLineBytes/2)
}

// clip cuts s to at most n bytes on a rune boundary, marking the cut.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func jsonEqual(a, b any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ab) == string(bb)
}

func unionSortedKeys(a, b map[string]any) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
