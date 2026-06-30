//go:build e2e

package agents

// Hypothesis-driven investigation E2E.
//
// Goal: prove the react_3 "hypothesis-first" discipline (PR: hypothesis-driven
// investigation + tighter classifier) actually *changes agent behaviour* on a
// real investigation — not just that the prompt renders the machinery (that is
// covered by the unit-level TestReAct3HypothesisModeFence).
//
// Why a benchmark fixture: the fixture self-bootstraps a real failure via its
// before_test hook (kubectl apply into a fixture-owned namespace), the agent
// investigates it for real, and after_test tears it down. We reuse
// 68_cascading_failures because it is a *chain-of-causation* scenario: the
// proximate symptom (payment-processor pod failures) is downstream of the true
// root cause (auth-service losing its Redis connection). A guess-first agent
// stops at the proximate symptom; a hypothesis-first agent must enumerate
// candidate causes, refute the decoy, and confirm the distal one.
//
// Two assertion tiers:
//
//  1. OUTCOME — the final answer must attribute the *distal* cause (auth/Redis)
//     and not stop at the payment-processor symptom. Checked via WantContainsAny
//     (cheap) + WantLLMClaims (LLM-judge, strict).
//
//  2. PROCESS — the model's own notebook (persisted to llm_conversation_agent
//     under agent_name='notebook') must contain a filled-in Hypothesis Tree with
//     at least one resolved status (SUPPORTED/REFUTED/INCONCLUSIVE) and
//     scenario-specific hypotheses. This is what distinguishes "followed the
//     discipline" from "got lucky on the answer" — and the scenario tokens prove
//     the tree is model-authored, not an echo of the prompt's template example.
//
// A/B note: run the same fixture with react_3 / hypothesis mode DISABLED and the
// notebook will not contain a hypothesis tree — that contrast is the cleanest
// causal attribution of the behaviour to this change. This test skips (rather
// than fails) when hypothesis mode is off, so it is a no-op on non-react_3 setups.
//
// Gated on the same env as the other fixture tests (TEST_ACCOUNT/USER/TENANT +
// reachable kubectl), PLUS react_3/hypothesis mode being enabled.

import (
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hypothesisModeActive reports whether react_3 (which carries the hypothesis
// notebook discipline) is enabled for this process. The hypothesis tree only
// renders / is exercised under react_3, so the process-tier assertions below
// are meaningless otherwise.
func hypothesisModeActive() bool {
	return config.Config.LlmServerReAct3Enabled || config.Config.LlmServerRewooToReact3Enabled
}

// pollNotebookForMessage returns the model-authored react_3 notebook for a
// message. The notebook is persisted to llm_conversation_agent (agent_name=
// 'notebook'), patched in place across turns, so we take the most-recent row.
// Polls because persistence happens on the planning loop, slightly after the
// HTTP response returns.
func pollNotebookForMessage(t *testing.T, convID, msgID string) string {
	t.Helper()
	db, err := common.GetDatabaseManager(common.Metastore)
	require.NoError(t, err)

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		var notebooks []string
		err = db.Db.Select(&notebooks, `
			SELECT COALESCE(response, '') FROM llm_conversation_agent
			WHERE conversation_id = $1::uuid AND message_id = $2::uuid AND agent_name = 'notebook'
			ORDER BY created_at DESC`, convID, msgID)
		require.NoError(t, err)
		for _, nb := range notebooks {
			if strings.TrimSpace(nb) != "" {
				return nb
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return ""
}

// containsAnyFold reports whether s contains any of needles (case-insensitive).
func containsAnyFold(s string, needles ...string) bool {
	lower := strings.ToLower(s)
	for _, n := range needles {
		if strings.Contains(lower, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

// TestK8sAgent_Fixture_HypothesisDrivenRCA drives the cascading-failure fixture
// and asserts both the correct distal root cause (outcome) and a model-authored
// hypothesis tree that resolved at least one candidate (process).
func TestK8sAgent_Fixture_HypothesisDrivenRCA(t *testing.T) {
	skipIfNoFixtureEnv(t)
	skipIfNoKubectl(t)
	if !hypothesisModeActive() {
		t.Skip("skipping: hypothesis mode requires react_3 " +
			"(set LLM_SERVER_REWOO_TO_REACT3_ENABLED=true or LLM_SERVER_REACT3_ENABLED=true)")
	}

	f := LoadFixture(t, fixturePath("nubi", "68_cascading_failures"))
	f.Setup(t)

	tc := f.TestCase
	// Tool-use floor: a real multi-service trace, not a one-shot guess.
	tc.WantAnyToolMatching = []string{"logs", "loki", "kubectl", "describe"}
	tc.WantMinToolCalls = 3
	// Outcome (cheap): the answer must name the distal cause, not just the symptom.
	tc.WantContainsAny = []string{"redis", "auth"}
	// Outcome (strict, LLM-judge): the fixture ground truth PLUS an explicit
	// discrimination claim that the answer reaches past the proximate symptom.
	tc.WantLLMClaims = append([]string{}, f.YAML.ExpectedOutput...)
	tc.WantLLMClaims = append(tc.WantLLMClaims,
		"The answer identifies auth-service losing its Redis connection as the upstream root cause, "+
			"rather than concluding the payment-processor itself is the root cause.")

	agent := f.Agent(t, newK8sDebugAgent(os.Getenv("TEST_ACCOUNT")))
	runTestMinimal(t, agent, tc)

	// ---- PROCESS tier: the model actually worked a hypothesis tree ----
	convID, msgID := scenarioLastMsgID(t, tc.SessionId, tc.UserId)
	notebook := pollNotebookForMessage(t, convID, msgID)
	t.Logf("[hypothesis-rca] model notebook (%d chars):\n%s", len(notebook), notebook)

	require.NotEmpty(t, notebook,
		"react_3 notebook was never persisted — the agent did not exercise the notebook discipline")

	// Structure: the notebook adopted the hypothesis-first sections.
	assert.True(t, containsAnyFold(notebook, "hypothesis tree", "## hypothesis"),
		"notebook must contain a Hypothesis Tree section (hypothesis-first discipline)")
	assert.True(t, containsAnyFold(notebook, "## scope", "scope"),
		"notebook must contain a Scope section (target/symptom resolved before hypothesizing)")

	// Resolution: at least one hypothesis reached a terminal status — i.e. the
	// agent confirmed/ruled out candidates instead of dumping status.
	assert.True(t, containsAnyFold(notebook, "SUPPORTED", "REFUTED", "INCONCLUSIVE"),
		"notebook must show at least one resolved hypothesis status (SUPPORTED/REFUTED/INCONCLUSIVE)")

	// Authorship: scenario-specific candidates prove the tree is the model's,
	// not an echo of the prompt's generic template example (which references
	// "traffic surge" / "recent deploy", never redis/auth/payment).
	assert.True(t, containsAnyFold(notebook, "redis", "auth", "payment"),
		"notebook hypotheses must reference the actual scenario entities (model-authored tree)")
}
