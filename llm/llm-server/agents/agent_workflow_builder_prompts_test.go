package agents

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// updateGolden regenerates the golden prompt snapshots in testdata/prompt_golden/.
// Run once before refactoring to capture the baseline:
//
//	go test ./agents -run TestWorkflowBuilderPromptGolden -update-golden
//
// The #31493 prompt-fragment extraction must leave these five pure prompt
// functions byte-identical (the only intended behavior change — generatePlan
// gaining the account_id-UUID rule — lives in an inline method, not here).
var updateGolden = flag.Bool("update-golden", false, "regenerate prompt golden snapshots")

// goldenPromptCases maps a snapshot name to the current output of each pure
// (no ctx / no network) prompt-producing function. Placeholder args keep the
// snapshot stable and focused on the static rule text.
func goldenPromptCases() map[string]string {
	schema := getWorkflowSchema()
	return map[string]string{
		"workflow_schema":      schema,
		"build_prompt":         getBuildSystemPrompt("INTENT_PLACEHOLDER", "PLAN_PLACEHOLDER", schema),
		"edit_prompt":          getEditSystemPrompt("ERROR_PLACEHOLDER", "EXEC_ID_PLACEHOLDER", schema),
		"planning_context":     getWorkflowPlanningContext(),
		"clarification_prompt": getClarificationSystemPrompt("ENV_PLACEHOLDER", "CONFIGS_PLACEHOLDER", "INTENT_PLACEHOLDER"),
	}
}

func TestWorkflowBuilderPromptGolden(t *testing.T) {
	dir := filepath.Join("testdata", "prompt_golden")

	for name, got := range goldenPromptCases() {
		path := filepath.Join(dir, name+".txt")

		if *updateGolden {
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, os.WriteFile(path, []byte(got), 0o644))
			t.Logf("wrote golden %s (%d bytes)", path, len(got))
			continue
		}

		want, err := os.ReadFile(path)
		require.NoError(t, err, "missing golden file %s — run: go test ./agents -run TestWorkflowBuilderPromptGolden -update-golden", path)
		// Normalize CRLF→LF so a Windows checkout with core.autocrlf doesn't
		// spuriously fail against the LF-committed golden files.
		assert.Equal(t, strings.ReplaceAll(string(want), "\r\n", "\n"), strings.ReplaceAll(got, "\r\n", "\n"),
			"%s prompt changed; re-run with -update-golden and review the diff before committing", name)
	}
}

// TestWorkflowBuilderPromptEngineRules asserts the prompts carry the
// post-#31494 runbook-engine rules, so they cannot silently drift back to the
// stale wording. Each fact maps to a specific runbook commit:
//   - transitive depends_on        → runbook a8152d0b7f
//   - switch SKIPPED propagation    → runbook 43144e3d9b
//   - whole-value non-string templates (MCP args/headers, etc.) → runbook 2aae0a1167
func TestWorkflowBuilderPromptEngineRules(t *testing.T) {
	schema := getWorkflowSchema()
	build := getBuildSystemPrompt("i", "p", schema)
	edit := getEditSystemPrompt("e", "x", schema)
	planning := getWorkflowPlanningContext()

	// (a) transitive depends_on — both authoring prompts must say a direct edge
	// is not required when the dep is transitively reachable.
	assert.Contains(t, build, "transitive reachability", "build prompt missing the transitive depends_on rule (#31494)")
	assert.Contains(t, edit, "transitively-reachable", "edit prompt missing the transitive depends_on rule (#31494)")

	// (b) switch SKIPPED fan-in — planning context must explain propagation.
	assert.Contains(t, planning, "SWITCH FAN-IN", "planning context missing the switch SKIPPED fan-in rule (#31494)")
	assert.Contains(t, planning, "stamped SKIPPED", "planning context missing switch-unselected SKIPPED note (#31494)")

	// (c) whole-value templates for non-string params (covers MCP args/headers).
	assert.Contains(t, planning, "WHOLE-VALUE TEMPLATES FOR NON-STRING PARAMS", "planning context missing whole-value template rule (#31494)")
}
