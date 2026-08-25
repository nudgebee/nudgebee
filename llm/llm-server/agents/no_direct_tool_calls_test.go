package agents

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNoDirectToolCallsInAgents fails when an agent invokes NBTool.Call itself
// instead of going through core.CallTool.
//
// This guard is the actual fix for the class of bug it was written for.
// llm_conversation_tool_calls rows and the tool Prometheus counters are written
// only by the ReAct executor's callback handler, so any agent that drives its
// own tool calls loses both — silently, with nothing failing and nothing logged.
// resource_search lost six months of telemetry that way when it became a custom
// agent (commit c79b98c63c, 2026-02-06); fetch_logs, metrics, traces, visualizer,
// websearch and unified_search never had any. Converting them is a one-time
// cleanup — without this test the next custom agent reopens the same hole.
//
// If a genuinely new reason to bypass core.CallTool appears, the right move is
// to widen CallTool, not to add an exemption here.
//
// The match is on the selector name alone, so an unrelated `x.Call(...)` (an RPC
// or gRPC client, say) would also trip it. That is deliberate: name-matching
// needs no type information, and a false positive fails loudly here rather than
// silently in production. Should one appear, narrow it by receiver type via
// go/types rather than allow-listing the file.
func TestNoDirectToolCallsInAgents(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read agents dir: %v", err)
	}

	var violations []string
	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Call" {
				return true
			}
			// Receiver name is enough to describe the site; the assertion
			// message carries the fix.
			recv := "<expr>"
			if ident, ok := sel.X.(*ast.Ident); ok {
				recv = ident.Name
			}
			violations = append(violations,
				filepath.Join("agents", name)+":"+
					strconv.Itoa(fset.Position(sel.Sel.Pos()).Line)+" "+recv+".Call(...)")
			return true
		})
	}

	assert.Empty(t, violations,
		"agents must invoke tools via core.CallTool(toolCtx, tool, req) — a direct NBTool.Call "+
			"writes no llm_conversation_tool_calls row and emits no tool metrics, making the "+
			"invocation invisible to the conversation UI and to latency analysis")
}
