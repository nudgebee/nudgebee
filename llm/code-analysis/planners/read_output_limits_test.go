package planners

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/langchaingo/llms"

	"nudgebee/code-analysis-agent/tools"
	"nudgebee/code-analysis-agent/tools/core"
)

// The read tool bounds what it returns and then states it ("lines X-Y of N").
// The planner has its own limits on any tool output, in two places: when the
// result is formatted, and again when it is put into the conversation. If
// either is tighter than the tool's, the planner cuts a read the tool has
// already described, and the header is wrong about what the model received.
// This pins the limits against each other: the largest read the tool can
// produce must reach the model untouched.
func TestABoundedReadReachesTheModelWhole(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{"many short lines hit the line limit", "x := compute(y)"},
		{"long lines hit the character limit", strings.Repeat("abcdefghij", 30)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			var b strings.Builder
			for i := 0; i < 3000; i++ {
				fmt.Fprintf(&b, "%s\n", tc.line)
			}
			if err := os.WriteFile(filepath.Join(dir, "big.go"), []byte(b.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			tool := tools.NewFileViewTool(dir)
			resp := tool.Execute(context.Background(), map[string]any{"file_path": "big.go", "working_directory": dir})
			if resp.Status != "success" {
				t.Fatalf("read failed: %s", resp.Error)
			}

			p := NewReActPlanner(nil, nil, 10)
			formatted := p.formatObservation(tool, resp)
			if formatted != resp.Observation {
				t.Fatalf("formatting changed a read the read tool had already bounded: %d chars in, %d out; omission marker present: %v",
					len(resp.Observation), len(formatted), strings.Contains(formatted, "omitted"))
			}

			// What the model is sent: the tool message the planner appends.
			sent := sentToolOutput(p, Step{Number: 1, Action: tool.Name(), Status: "completed", Observation: formatted, ToolCallID: "c1"})
			if sent != resp.Observation {
				t.Fatalf("the conversation holds a cut copy of a read the read tool had already bounded: %d chars from the tool, %d sent; cut marker present: %v",
					len(resp.Observation), len(sent), strings.Contains(sent, "omitted"))
			}
			if !strings.HasPrefix(sent, "[lines 1-") || !strings.Contains(sent, "of 3000]") {
				t.Fatalf("no line header on what the model receives: %.60s", sent)
			}
			if !strings.Contains(sent, "TRUNCATED") || !strings.Contains(sent, "start_line=") {
				t.Fatalf("the note saying where to continue did not reach the model: %s", sent[len(sent)-200:])
			}
		})
	}
}

// sentToolOutput returns what the model is sent for one completed step.
func sentToolOutput(p *ReActPlanner, step Step) string {
	sent := ""
	for _, m := range p.updateConversationMessagesMulti(nil, []Step{step}) {
		for _, part := range m.Parts {
			if tr, ok := part.(llms.ToolCallResponse); ok {
				sent = tr.Content
			}
		}
	}
	return sent
}

// A long tool result is trimmed once, with its end kept and the gap stated. It
// used to be trimmed twice: the first pass kept the head and the tail, and the
// second cut everything past 15,000 characters — the tail the first had kept —
// and said only "[truncated]". The end of a result is where a test run prints
// what failed and where a search tool says how to narrow the search.
func TestALongToolResultKeepsItsEndAndStatesTheGap(t *testing.T) {
	tests := []struct {
		name  string
		lines int
		width int
	}{
		{"more lines than the line limit", 3000, 40},
		{"few lines, more characters than the character limit", 300, 150},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			for i := 1; i <= tc.lines; i++ {
				fmt.Fprintf(&b, "row %d %s\n", i, strings.Repeat("x", tc.width))
			}
			b.WriteString("FAIL: the last line says what went wrong")
			p := NewReActPlanner(nil, nil, 10)
			tool := &probeTool{name: "cli"}

			formatted := p.formatObservation(tool, core.NBToolResponse{Status: "success", Observation: b.String()})
			sent := sentToolOutput(p, Step{Number: 1, Action: "cli", Status: "completed", Observation: formatted, ToolCallID: "c1"})

			if sent != formatted {
				t.Fatalf("the result was trimmed a second time when put into the conversation: %d chars formatted, %d sent", len(formatted), len(sent))
			}
			if len(sent) > maxObservationChars {
				t.Fatalf("%d characters sent, limit is %d", len(sent), maxObservationChars)
			}
			if !strings.HasPrefix(sent, "row 1 ") {
				t.Fatalf("the start of the result is gone: %.40s", sent)
			}
			if !strings.HasSuffix(sent, "FAIL: the last line says what went wrong") {
				t.Fatalf("the end of the result is gone: %s", sent[len(sent)-80:])
			}
			if !strings.Contains(sent, "omitted ...]") {
				t.Fatal("the result does not say that part of it was left out")
			}
			if strings.Contains(sent, "[truncated]") {
				t.Fatal("the result carries a bare [truncated] with no size")
			}
		})
	}
}

// Error text does not pass through formatObservation; the same limit, with the
// same stated gap, applies to it when it is put into the conversation.
func TestALongToolErrorIsTrimmedWithAStatedGap(t *testing.T) {
	p := NewReActPlanner(nil, nil, 10)
	long := "boom: " + strings.Repeat("e", 3*maxObservationChars) + " :end of error"

	sent := sentToolOutput(p, Step{Number: 1, Action: "cli", Status: "failed", Error: long, ToolCallID: "c1"})

	if len(sent) > maxObservationChars {
		t.Fatalf("%d characters sent, limit is %d", len(sent), maxObservationChars)
	}
	if !strings.HasPrefix(sent, "Error: boom: ") || !strings.Contains(sent, "chars omitted ...]") {
		t.Fatalf("want the start of the error and a stated gap, got: %.60s", sent)
	}
}

// The planner puts its own notes around a result after formatting it. A result
// already trimmed to the limit must still fit with both notes on it; otherwise
// it is trimmed a second time, through the note.
func TestAFormattedResultHasRoomForThePlannersNotes(t *testing.T) {
	p := NewReActPlanner(nil, nil, 10)
	// Few enough lines that the line limit does not apply, so the result is
	// trimmed by characters and comes out as long as formatting allows.
	long := strings.Repeat(strings.Repeat("0123456789", 20)+"\n", 400)
	formatted := p.formatObservation(&probeTool{name: "file_find"}, core.NBToolResponse{Status: "success", Observation: long})
	if limit := maxObservationChars - observationNoteReserve; len(formatted) > limit || len(formatted) < limit-100 {
		t.Fatalf("the formatted result is %d characters; this test needs one at the formatting limit of %d", len(formatted), limit)
	}

	withNotes := cachedResultNote("file_find", 12345) + formatted + repeatCallWarning
	sent := sentToolOutput(p, Step{Number: 1, Action: "file_find", Status: "completed", Observation: withNotes, ToolCallID: "c1"})

	if sent != withNotes {
		t.Fatalf("a result with the planner's notes on it was trimmed again: %d chars with notes, %d sent, limit %d",
			len(withNotes), len(sent), maxObservationChars)
	}
	if !strings.HasSuffix(sent, "Try a different approach.") {
		t.Fatalf("the repeat-call warning did not reach the model: %s", sent[len(sent)-80:])
	}
}
