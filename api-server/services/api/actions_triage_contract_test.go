package api

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// llmServerTriageToolPath is the llm-server file that POSTs to /rpc/triage with
// a service token, bypassing the app gateway entirely.
const llmServerTriageToolPath = "llm/llm-server/tools/tool_triage.go"

var (
	llmTriageCallRe = regexp.MustCompile(`doTriageActionRequest\(nbCtx, "([a-z0-9_]+)"`)
	caseLiteralRe   = regexp.MustCompile(`"([a-z0-9_]+)"`)
)

// TestTriageActionsCoverLLMServerCallers guards the /rpc/triage switch against
// the failure that removed event_get_triage in #36733: the handler was read as
// dead because it is absent from app/src/lib/actions.yaml, but actions.yaml
// only describes what the app gateway dispatches. llm-server calls these
// actions directly, and a deleted case surfaces to it as a 400
// "unknown action" that the agent swallows as a normal tool observation — so
// nothing fails loudly until someone reads the tool-call table.
//
// Deleting a case here is fine; it just has to be done together with its
// llm-server caller.
func TestTriageActionsCoverLLMServerCallers(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	require.NoError(t, err)

	toolSrc, err := os.ReadFile(filepath.Join(root, llmServerTriageToolPath))
	if err != nil {
		t.Skipf("llm-server tool source not available at %s: %v", llmServerTriageToolPath, err)
	}

	calls := llmTriageCallRe.FindAllSubmatch(toolSrc, -1)
	require.NotEmpty(t, calls, "no doTriageActionRequest call sites found in %s — has the helper been renamed?", llmServerTriageToolPath)

	handled := handledTriageActions(t)
	for _, m := range calls {
		action := string(m[1])
		assert.Contains(t, handled, action,
			"llm-server calls /rpc/triage action %q but handleTriageAction has no case for it; "+
				"restore the case or remove the caller in %s", action, llmServerTriageToolPath)
	}
}

// handledTriageActions returns the action names the handleTriageAction switch
// dispatches, read from the source so the test tracks the switch rather than a
// second hand-maintained list.
func handledTriageActions(t *testing.T) map[string]bool {
	t.Helper()

	src, err := os.ReadFile("actions_triage.go")
	require.NoError(t, err)

	body := string(src)
	start := strings.Index(body, "switch actionName {")
	require.GreaterOrEqual(t, start, 0, "handleTriageAction switch not found")
	end := strings.Index(body[start:], "\n\tdefault:")
	require.Greater(t, end, 0, "handleTriageAction switch has no default arm")

	// Match the exact one-tab indent gofmt gives the switch's own case labels,
	// so a nested switch inside a case body (its labels two tabs deep) cannot
	// contribute names and make this test pass on an action nothing dispatches.
	actions := map[string]bool{}
	for _, line := range strings.Split(body[start:start+end], "\n") {
		if !strings.HasPrefix(line, "\tcase ") {
			continue
		}
		for _, m := range caseLiteralRe.FindAllStringSubmatch(line, -1) {
			actions[m[1]] = true
		}
	}
	require.NotEmpty(t, actions)
	return actions
}
