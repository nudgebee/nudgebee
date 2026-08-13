package tools

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLastPipelineStages(t *testing.T) {
	cases := map[string][]string{
		"go build ./...":                         {"go build ./..."},
		"go build ./... 2>&1 | grep -v 'go: dl'": {"go build ./... 2>&1", "grep -v 'go: dl'"},
		"cd api && go build | grep -v noise":     {"go build", "grep -v noise"},
		"a | b; c":                               {"c"},
		"a || b | c":                             {"b", "c"},
		`grep "a|b" file`:                        {`grep "a|b" file`},
		"find . -name go.mod | head -5 | wc -l":  {"find . -name go.mod", "head -5", "wc -l"},
		`sh -c "echo hi; exit 2" | grep -v x`:    {`sh -c "echo hi; exit 2"`, "grep -v x"},
		"FOO=bar go test ./... 2>&1 | tail -20":  {"FOO=bar go test ./... 2>&1", "tail -20"},
		// Backslash escaping: `\"` must not toggle quote state, `\|` is not a pipe.
		`echo "a\"|b" | grep x`: {`echo "a\"|b"`, "grep x"},
		`echo a\| b | wc -l`:    {`echo a\| b`, "wc -l"},
	}
	for cmd, want := range cases {
		if got := lastPipelineStages(cmd); !reflect.DeepEqual(got, want) {
			t.Errorf("lastPipelineStages(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestStageBinary(t *testing.T) {
	cases := map[string]string{
		"go build ./... 2>&1": "go",
		"grep -v 'x'":         "grep",
		"/usr/bin/grep -rn f": "grep",
		"FOO=bar rg pattern":  "rg",
		"":                    "",
	}
	for stage, want := range cases {
		if got := stageBinary(stage); got != want {
			t.Errorf("stageBinary(%q) = %q, want %q", stage, got, want)
		}
	}
}

func TestExtractPipeStatus(t *testing.T) {
	out, codes := extractPipeStatus([]byte("real output\n\n" + pipeStatusMarker + " 1 0\n"))
	if want := []int{1, 0}; !reflect.DeepEqual(codes, want) {
		t.Fatalf("codes = %v, want %v", codes, want)
	}
	if string(out) != "real output" {
		t.Fatalf("output = %q, want %q", string(out), "real output")
	}

	raw := []byte("no marker here\n")
	out, codes = extractPipeStatus(raw)
	if codes != nil || string(out) != string(raw) {
		t.Fatalf("missing marker must return input unchanged, got %q / %v", out, codes)
	}
}

// TestPipelineVerdicts exercises the two signal inversions observed in production:
// a failing command piped through grep -v reported success (pipeline exit = grep's
// 0), and a clean command piped through grep -v reported exit 1 (grep found no
// lines). Both must now be judged by per-stage codes.
func TestPipelineVerdicts(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	tool := NewCLITool(t.TempDir())

	// Failing stage, grep filters everything through → pipeline exit 0 → was a false PASS.
	resp := tool.Execute(context.Background(), map[string]any{
		"command": `sh -c "echo 'compile error: bad import'; exit 2" | grep -v downloading`,
	})
	if resp.Status != "error" {
		t.Fatalf("failing first stage must be an error, got status=%q obs=%q", resp.Status, resp.Observation)
	}
	if !strings.Contains(resp.Observation, "sh=2") {
		t.Errorf("observation must carry per-stage codes, got: %q", resp.Observation)
	}
	if !strings.Contains(resp.Observation, "compile error: bad import") {
		t.Errorf("observation must carry the real error text, got: %q", resp.Observation)
	}
	// Error responses must keep their structured data: the fix verifier reads
	// exit codes from it, and failed commands are exactly where that matters.
	if data, ok := resp.Data.(map[string]any); !ok || data["result"] == nil {
		t.Errorf("error response must carry CLIOutput data, got %T", resp.Data)
	}

	// Clean stage, grep matches nothing → pipeline exit 1 → was a confusing "exit 1 but succeeded".
	resp = tool.Execute(context.Background(), map[string]any{
		"command": `sh -c "exit 0" | grep -v downloading`,
	})
	if resp.Status == "error" {
		t.Fatalf("clean first stage with no-match grep must succeed, got obs=%q", resp.Observation)
	}
	if !strings.Contains(resp.Observation, "grep=1 (no matches)") {
		t.Errorf("grep no-match must be annotated honestly, got: %q", resp.Observation)
	}

	// All stages clean.
	resp = tool.Execute(context.Background(), map[string]any{
		"command": `echo hello | grep hello`,
	})
	if resp.Status == "error" || !strings.Contains(resp.Observation, "hello") {
		t.Fatalf("clean pipeline must succeed with output, got status=%q obs=%q", resp.Status, resp.Observation)
	}
}

func TestKillSwitchRestoresOldBehavior(t *testing.T) {
	t.Setenv("NB_CLI_STAGE_EXITS", "false")
	tool := NewCLITool(t.TempDir())
	resp := tool.Execute(context.Background(), map[string]any{
		"command": `sh -c "echo err; exit 2" | grep -v downloading`,
	})
	// Old (dishonest) behavior: pipeline exit is grep's 0 → success. The kill
	// switch exists precisely to restore this if the wrapper misbehaves somewhere.
	if resp.Status == "error" {
		t.Fatalf("kill switch must restore last-stage-exit semantics, got obs=%q", resp.Observation)
	}
	if strings.Contains(resp.Observation, "Pipeline stage exits") {
		t.Errorf("kill switch must disable stage reporting, got: %q", resp.Observation)
	}
}

func TestHeadTailTruncateRuneSafe(t *testing.T) {
	// Multi-byte content sized so naive byte slicing would cut mid-rune.
	s := strings.Repeat("é", 4000) // 2 bytes each = 8000 bytes
	out := headTailTruncate(s, 3*1024, 1024, "", "")
	if !utf8.ValidString(out) {
		t.Fatal("truncated output must remain valid UTF-8")
	}
	if !strings.Contains(out, "output truncated") {
		t.Fatal("truncation marker missing")
	}
}

func TestHeadTailTruncateAndSpill(t *testing.T) {
	tool := NewCLITool(t.TempDir())
	// ~40KB of numbered lines with a distinctive tail — the error at the end must survive.
	resp := tool.Execute(context.Background(), map[string]any{
		"command": `sh -c 'for i in $(seq 1 4000); do echo "line $i"; done; echo "FINAL ERROR MARKER"'`,
	})
	if resp.Status == "error" {
		t.Fatalf("command should succeed, got: %q", resp.Error)
	}
	if !strings.Contains(resp.Observation, "FINAL ERROR MARKER") {
		t.Errorf("tail of output must survive truncation")
	}
	if !strings.Contains(resp.Observation, "output truncated") {
		t.Errorf("truncation must be stated, got %d bytes obs", len(resp.Observation))
	}
	// Full output must be spilled and referenced.
	data, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data shape %T", resp.Data)
	}
	out, ok := data["result"].(*CLIOutput)
	if !ok {
		t.Fatalf("unexpected result shape %T", data["result"])
	}
	if out.SpillPath == "" {
		t.Fatal("SpillPath must be set for truncated output")
	}
	spilled, err := os.ReadFile(out.SpillPath)
	if err != nil {
		t.Fatalf("spill file unreadable: %v", err)
	}
	if !strings.Contains(string(spilled), "line 1\n") || !strings.Contains(string(spilled), "FINAL ERROR MARKER") {
		t.Errorf("spill file must contain the full output")
	}
	if !strings.Contains(out.Stdout, out.SpillPath) {
		t.Errorf("truncation marker must reference the spill path, got: %q", out.Stdout[3000:3400])
	}
}
