package core

// Opt-in live check of the classification API on real cheap models. It drives
// Classify end to end (prompt, call options, strict matching, retries) with the
// LLM call routed straight to the Google AI adapter, so no DB or config is needed.
// Credentials come from the environment and are never committed.
import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/llms/googleai"
	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

type liveClassifyCase struct {
	input string
	want  string
}

// Synthetic requests in the shape of real traffic, including a bare pasted error.
var liveIntentCases = []liveClassifyCase{
	{"Explain what this automation does.", "ANSWER"},
	{"Why did the last run fail?", "ANSWER"},
	{"How can I test this workflow?", "ANSWER"},
	{"Is this webhook condition correct?", "ANSWER"},
	{"Add a Slack notification after the approval step.", "EDIT"},
	{"Can you change the schedule to run daily at 6 AM?", "EDIT"},
	{"Remove the email task.", "EDIT"},
	{"Fix the error in step 2 and save it.", "EDIT"},
	{"Error: failed to parse script output as JSON: invalid character 'T' looking for beginning of value", "EDIT"},
	{"Create a workflow that posts to Slack when a pull request is merged.", "CREATE"},
	{"Build a new automation that lists unused volumes every Friday.", "CREATE"},
}

var liveCategoryCases = []liveClassifyCase{
	{"Why is the checkout service slow since this morning?\n\nTools Executed: logs, prometheus", "investigation"},
	{"Pods in the payments namespace keep restarting, find the cause.\n\nTools Executed: k8s_orchestrator", "investigation"},
	{"Hi, what can you do?\n\nTools Executed: ", "others"},
	{"Summarize our monthly cloud spend by team.\n\nTools Executed: finops", "others"},
}

func liveIntentTask() ClassificationTask {
	return ClassificationTask{
		Name: "live_turn_intent",
		Instructions: `You classify a user's message in an automation-builder chat into one of three intents. There IS currently an automation open in the editor.
A request to change or fix the open automation is EDIT, even when phrased as a question. A pasted error message with no question about why is a request to repair it: EDIT.
A question that only asks to understand or diagnose, with no change requested, is ANSWER. Building a brand-new automation is CREATE.`,
		Options: []ClassificationOption{
			{Label: "ANSWER", Description: "The user wants information, an explanation, a diagnosis or status, with no change requested."},
			{Label: "EDIT", Description: "The user wants to change or repair the automation that is open."},
			{Label: "CREATE", Description: "The user wants to build a new automation from scratch."},
		},
	}
}

func liveCategoryTask() ClassificationTask {
	return ClassificationTask{
		Name:         "live_category",
		Instructions: "Classify this user message, using the tools that were executed for it, into one of the categories.",
		Options: []ClassificationOption{
			{Label: "investigation", Description: "Troubleshooting, debugging, searching logs, analyzing metrics, checking traces, service status, slowness, or errors."},
			{Label: "others", Description: "A general query, greeting, help request, or any other topic not directly related to active investigation."},
		},
	}
}

// TestClassifyLive runs Classify against each model in CLASSIFY_LIVE_MODELS
// (comma-separated, e.g. "gemini-3.5-flash-lite,gemini-2.5-flash"), using
// CLASSIFY_LIVE_KEY and CLASSIFY_LIVE_ENDPOINT for the Google AI adapter.
func TestClassifyLive(t *testing.T) {
	models := os.Getenv("CLASSIFY_LIVE_MODELS")
	if models == "" {
		t.Skip("set CLASSIFY_LIVE_MODELS to opt in to paid inference")
	}
	key, endpoint := os.Getenv("CLASSIFY_LIVE_KEY"), os.Getenv("CLASSIFY_LIVE_ENDPOINT")
	require.NotEmpty(t, key)
	require.NotEmpty(t, endpoint)

	for model := range strings.SplitSeq(models, ",") {
		model = strings.TrimSpace(model)
		t.Run(model, func(t *testing.T) {
			client, err := googleai.New(context.Background(), googleai.WithAPIKey(key), googleai.WithBaseURL(endpoint), googleai.WithDefaultModel(model))
			require.NoError(t, err)
			calls := 0
			old := classificationGenerate
			classificationGenerate = func(rc *security.RequestContext, _, _, _, _, _ string, _ bool, msgs []llms.MessageContent, _ bool, opts ...llms.CallOption) (*llms.ContentResponse, error) {
				calls++
				ctx, cancel := context.WithTimeout(rc.GetContext(), 60*time.Second)
				defer cancel()
				return client.GenerateContent(ctx, msgs, withModelThinking(model, opts)...)
			}
			t.Cleanup(func() { classificationGenerate = old })

			for _, suite := range []struct {
				task  ClassificationTask
				cases []liveClassifyCase
			}{{liveIntentTask(), liveIntentCases}, {liveCategoryTask(), liveCategoryCases}} {
				correct, invalid := 0, 0
				var latencies []time.Duration
				for _, c := range suite.cases {
					d, err := Classify(testRequestContext(), ClassificationCall{AgentId: "live_test"}, suite.task, c.input)
					require.NoError(t, err, c.input)
					latencies = append(latencies, d.Latency)
					if !d.Valid {
						invalid++
						t.Logf("%s: invalid answer %q for %q", suite.task.Name, d.Raw, c.input)
						continue
					}
					if d.Label() == c.want {
						correct++
					} else {
						t.Logf("%s: got %s want %s for %q", suite.task.Name, d.Label(), c.want, c.input)
					}
				}
				t.Logf("%s on %s: %d/%d correct, %d invalid, max latency %s",
					suite.task.Name, model, correct, len(suite.cases), invalid, maxDuration(latencies))
				assert.Zero(t, invalid, "every answer must be a valid label")
				assert.GreaterOrEqual(t, correct, len(suite.cases)-1, "at most one miss per task")
			}
			t.Logf("%s: %d LLM calls for %d decisions", model, calls, len(liveIntentCases)+len(liveCategoryCases))
		})
	}
}

// withModelThinking applies the per-model thinking clamp that GenerateAndTrackLLMContent
// applies in production, which this test bypasses: a level the model does not accept is
// adjusted, or dropped when the model takes none.
func withModelThinking(model string, opts []llms.CallOption) []llms.CallOption {
	combined := llms.CallOptions{}
	for _, opt := range opts {
		opt(&combined)
	}
	level, ok := combined.Metadata["ThinkingLevel"].(string)
	if !ok {
		return opts
	}
	clamped := clampThinkingLevel(model, level)
	if clamped == "" {
		clamped = "none"
	}
	return append(opts, WithThinkingLevel(clamped))
}

func maxDuration(ds []time.Duration) time.Duration {
	var m time.Duration
	for _, d := range ds {
		if d > m {
			m = d
		}
	}
	return m.Round(time.Millisecond)
}
