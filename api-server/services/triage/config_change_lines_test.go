package triage

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// flagsFile is a ~4.9 KB flags file held in one ConfigMap key. Its
// "loadGeneratorVUs" flag sits past byte 2048, which is where the agent caps
// each side of a ConfigMap diff — so an edit to it is absent from the diff block
// and only the full objects carry it.
func flagsFile(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/flagd.json")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if i := strings.Index(s, `"loadGeneratorVUs"`); i < 2048 {
		t.Fatalf("fixture must hold the flag past byte 2048, found at %d", i)
	}
	return s
}

func withVUs(t *testing.T, file, vus string) string {
	t.Helper()
	const from = "\"loadGeneratorVUs\": {\n      \"defaultVariant\": \"5\","
	if !strings.Contains(file, from) {
		t.Fatal("fixture no longer has the expected loadGeneratorVUs block")
	}
	return strings.Replace(file, from, "\"loadGeneratorVUs\": {\n      \"defaultVariant\": \""+vus+"\",", 1)
}

func configMap(data map[string]any) map[string]any {
	return map[string]any{"metadata": map[string]any{"name": "flags"}, "data": data}
}

// evidences builds an evidences column the way the agent writes it: the json
// block's data is a JSON-encoded string holding obj / oldObj, and the diff block
// carries head-capped per-path values.
func evidences(t *testing.T, kind string, before, after map[string]any, paths []PathChange) json.RawMessage {
	t.Helper()
	var blocks []map[string]any
	if before != nil {
		payload, _ := json.Marshal(map[string]any{"kind": kind, "obj": after, "oldObj": before, "operation": "update"})
		blocks = append(blocks, map[string]any{"type": "json", "data": string(payload)})
	}
	var values []map[string]any
	for _, p := range paths {
		values = append(values, map[string]any{"path": p.Path, "old": p.Old, "new": p.New})
	}
	blocks = append(blocks, map[string]any{"type": "diff", "data": map[string]any{
		"resource_name":  strings.ToLower(kind) + "/demo/flags.yaml",
		"updated_values": values,
	}})
	b, err := json.Marshal(blocks)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// headCapped reproduces the agent's 2048-byte head cap on a ConfigMap value.
func headCapped(s string) string {
	return fmt.Sprintf("%s\n... [truncated: %d of %d bytes shown]", s[:2048], 2048, len(s))
}

func TestChangeLinesNamesAnEditPastTheCappedHead(t *testing.T) {
	before := flagsFile(t)
	after := withVUs(t, before, "25")
	ev := evidences(t, "ConfigMap",
		configMap(map[string]any{"demo.flagd.json": before}),
		configMap(map[string]any{"demo.flagd.json": after}),
		[]PathChange{{Path: "data.demo.flagd.json", Old: headCapped(before), New: headCapped(after)}})

	rc, ok := ParseRecordedChange(ev)
	if !ok || rc.Kind != "configmap" {
		t.Fatalf("ParseRecordedChange = %+v, %v", rc.Kind, ok)
	}
	lines := ChangeLines(rc, rc, 1)
	if len(lines) != 1 {
		t.Fatalf("want one line for one edited line, got %q", lines)
	}
	got := lines[0]
	for _, want := range []string{"data.demo.flagd.json", `"loadGeneratorVUs": {`, `"defaultVariant": "5"`, `"defaultVariant": "25"`} {
		if !strings.Contains(got, want) {
			t.Errorf("line %q is missing %q", got, want)
		}
	}
}

func TestChangeLinesSaysWhenOnlyTheCappedSummaryWasRecorded(t *testing.T) {
	before := flagsFile(t)
	after := withVUs(t, before, "25")
	ev := evidences(t, "ConfigMap", nil, nil,
		[]PathChange{{Path: "data.demo.flagd.json", Old: headCapped(before), New: headCapped(after)}})

	rc, ok := ParseRecordedChange(ev)
	if !ok {
		t.Fatal("a diff block alone is still a recorded change")
	}
	lines := ChangeLines(rc, rc, 1)
	if len(lines) != 1 || !strings.Contains(lines[0], "changed past the part of the value this event recorded") {
		t.Fatalf("identical capped heads must say the edit was not recorded, got %q", lines)
	}
}

func TestChangeLinesWorkloadSpec(t *testing.T) {
	deploy := func(cpu string) map[string]any {
		return map[string]any{
			"metadata": map[string]any{"name": "catalog", "annotations": map[string]any{"deployment.kubernetes.io/revision": cpu}},
			"spec": map[string]any{"replicas": 1.0, "template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": "app", "resources": map[string]any{"limits": map[string]any{"cpu": cpu}}}},
			}}},
			"status": map[string]any{"readyReplicas": cpu},
		}
	}
	ev := evidences(t, "Deployment", deploy("100m"), deploy("15m"), nil)
	rc, _ := ParseRecordedChange(ev)
	lines := ChangeLines(rc, rc, 1)
	want := "spec.template.spec.containers[0].resources.limits.cpu: 100m → 15m"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %q, want [%q] (status and metadata are not the change)", lines, want)
	}
}

func TestChangeLinesNetChangeAcrossFoldedEdits(t *testing.T) {
	v1 := configMap(map[string]any{"LOAD": "5"})
	v2 := configMap(map[string]any{"LOAD": "25"})
	v3 := configMap(map[string]any{"LOAD": "50"})
	first, _ := ParseRecordedChange(evidences(t, "ConfigMap", v1, v2, nil))
	last, _ := ParseRecordedChange(evidences(t, "ConfigMap", v2, v3, nil))
	if got := ChangeLines(first, last, 2); len(got) != 1 || got[0] != "data.LOAD: 5 → 50" {
		t.Fatalf("two edits read as their net change, got %q", got)
	}

	reverted, _ := ParseRecordedChange(evidences(t, "ConfigMap", v2, v1, nil))
	got := ChangeLines(first, reverted, 2)
	if len(got) != 1 || got[0] != "edited 2 times; no net change across those edits" {
		t.Fatalf("an edit that was reverted must not read as the change, got %q", got)
	}
}

func TestChangeLinesAddedRemovedAndIngressAnnotations(t *testing.T) {
	before := configMap(map[string]any{"old.conf": "a\nb", "keep": "x"})
	after := configMap(map[string]any{"new.conf": "a\nb\nc", "keep": "x", "MODE": "fast"})
	rc, _ := ParseRecordedChange(evidences(t, "ConfigMap", before, after, nil))
	got := strings.Join(ChangeLines(rc, rc, 1), "\n")
	for _, want := range []string{"data.MODE added: fast", "data.new.conf added (3 lines)", "data.old.conf removed"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}

	ing := func(size, applied string) map[string]any {
		return map[string]any{
			"metadata": map[string]any{"annotations": map[string]any{
				"nginx.ingress.kubernetes.io/proxy-body-size": size,
				lastAppliedAnnotation:                         applied,
			}},
			"spec": map[string]any{"rules": []any{}},
		}
	}
	rc, _ = ParseRecordedChange(evidences(t, "Ingress", ing("8m", "{v1}"), ing("1m", "{v2}"), nil))
	lines := ChangeLines(rc, rc, 1)
	want := "metadata.annotations.nginx.ingress.kubernetes.io/proxy-body-size: 8m → 1m"
	if len(lines) != 1 || lines[0] != want {
		t.Fatalf("got %q, want [%q] (last-applied repeats the change)", lines, want)
	}
}

func TestChangeLinesCapsAndClipsOnRuneBoundary(t *testing.T) {
	before, after := map[string]any{}, map[string]any{}
	for i := 0; i < 12; i++ {
		k := fmt.Sprintf("K%02d", i)
		before[k] = strings.Repeat("é", 100)
		after[k] = strings.Repeat("ü", 100)
	}
	rc, _ := ParseRecordedChange(evidences(t, "ConfigMap", configMap(before), configMap(after), nil))
	lines := ChangeLines(rc, rc, 1)
	if len(lines) > maxChangeLines+1 {
		t.Fatalf("got %d lines, cap is %d plus the remainder line", len(lines), maxChangeLines)
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "… (+") {
		t.Errorf("a cut list must say how many were left out, last line %q", last)
	}
	size := 0
	for _, l := range lines[:len(lines)-1] {
		size += len(l)
		if !utf8.ValidString(l) {
			t.Errorf("clipped line is not valid UTF-8: %q", l)
		}
	}
	if size > maxChangeItemBytes {
		t.Errorf("lines total %d bytes, cap is %d", size, maxChangeItemBytes)
	}
}

func TestParseRecordedChangeRejectsWhatIsNotAChange(t *testing.T) {
	for name, ev := range map[string]string{
		"empty":       ``,
		"not json":    `nope`,
		"other block": `[{"type":"markdown","data":"hello"}]`,
		"json no obj": `[{"type":"json","data":"{\"kind\":\"ConfigMap\"}"}]`,
	} {
		if _, ok := ParseRecordedChange(json.RawMessage(ev)); ok {
			t.Errorf("%s: want ok=false", name)
		}
	}
}

// The stored objects are uncapped. Many scattered edits to a large value with
// repeated lines made the line matcher take minutes; past maxMatchedLines the
// change is named by where it runs and its first line.
func TestChangeLinesLargeValueWithManyEditsStaysFast(t *testing.T) {
	var b, a strings.Builder
	for i := 0; i < 30000; i++ {
		fmt.Fprintf(&b, "    \"k%d\": %d,\n  },\n", i, i)
		v := i
		if i%97 == 1 {
			v = -i
		}
		fmt.Fprintf(&a, "    \"k%d\": %d,\n  },\n", i, v)
	}
	start := time.Now()
	lines := valueChangeLines("data.big.json", b.String(), a.String())
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
	if len(lines) != 1 || !strings.Contains(lines[0], "lines 3-") || !strings.Contains(lines[0], `"k1": 1, → "k1": -1,`) {
		t.Fatalf("got %q", lines)
	}
}

// A newer agent cuts an over-cap value to its changed ranges, each headed by
// its line numbers. An edit that adds a line shifts those numbers; that is
// not part of the change.
func TestChangeLinesIgnoresExcerptHeaders(t *testing.T) {
	before := "... [truncated: showing lines 101-104 of 300]\n  a: 1\n  b: 2\n  c: 3\n  d: 4"
	after := "... [truncated: showing lines 101-105 of 301]\n  a: 1\n  b: 2\n  b2: 9\n  c: 3\n  d: 4"
	got := pathChangeLines([]PathChange{{Path: "data.app.yaml", Old: before, New: after}})
	if len(got) != 1 || got[0] != "data.app.yaml: added b2: 9" {
		t.Fatalf("got %q", got)
	}
}
