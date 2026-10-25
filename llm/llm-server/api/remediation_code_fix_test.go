package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/security"
)

// Two plans that said "nothing to change" for a fix in source while the alert kept firing.

// A worker service's OOM: the fix is a code change; the model put it in optional_hardening.
func workerOOMHardeningOnly() RemediationPlan {
	return RemediationPlan{RootCause: "command output is read unbounded into memory",
		Summary: "No executable actions are proposed; preventing recurrence requires a code patch via pull request.",
		OptionalHardening: []HardeningItem{{Target: "worker/fetch.go FetchOptions",
			Change: "set a bounded MaxOutputSize", Reason: "not required"}}}
}

// A leaky app's CrashLoopBackOff (OOM): the fix is in the Deployment's inline script.
func leakHardeningOnly() RemediationPlan {
	return RemediationPlan{RootCause: "the application's in-memory cache grows without bound",
		Summary:           "No CLI command can resolve an application-level unbounded memory leak.",
		OptionalHardening: []HardeningItem{{Target: "application cache", Change: "cache capacity bounds or an LRU/TTL eviction policy"}}}
}

func leakSourceFixOnly() RemediationPlan {
	return RemediationPlan{RootCause: "the application's in-memory cache grows without bound", Actions: []RemediationAction{{
		Action: "Bound the cache", Kind: RemediationKindFix, Confidence: 80,
		RequiredChange: &RequiredChange{Where: "deployment_source", Target: "Deployment demo/leaky-app container command script",
			Change: "bound the cache to 10000 entries with LRU eviction", Reason: "the cache is what grows until the OOM kill"}}}}
}

func stubFiring(t *testing.T, firing bool) {
	t.Helper()
	swap(t, &alertStillFiring, func(context.Context, string, string) bool { return firing })
	swap(t, &shownTargetState, &targetStateMemo{seen: map[string]time.Time{}})
}

// A firing alert's plan with no actions and only hardening is refused once (both real plans); sent again unchanged,
// it stands.
func TestFiringPlanRefusal_HardeningOnly(t *testing.T) {
	for name, plan := range map[string]RemediationPlan{"evt-worker-oom": workerOOMHardeningOnly(), "evt-leak": leakHardeningOnly()} {
		t.Run(name, func(t *testing.T) {
			stubFiring(t, true)
			err := firingPlanRefusal(context.Background(), "acct", name, plan)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "a change the incident needs is an action")
			assert.Contains(t, err.Error(), "required_change")
			assert.Contains(t, err.Error(), "offer a mitigation command that buys time")
			assert.NoError(t, firingPlanRefusal(context.Background(), "acct", name, plan), "the same plan again: the agent said why")
		})
	}
}

// The leaky app's plan written right but with only the source fix: refused once for the missing mitigation; with a mitigation
// that states its limit it is stored; a closed alert is not asked for one.
func TestFiringPlanRefusal_SourceFixNeedsAMitigation(t *testing.T) {
	stubFiring(t, true)
	plan := leakSourceFixOnly()
	err := firingPlanRefusal(context.Background(), "acct", "evt-leak", plan)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "also offer a mitigation command that buys time")

	withMitigation := leakSourceFixOnly()
	withMitigation.Actions = append(withMitigation.Actions, RemediationAction{Action: "Raise the memory limit", Kind: RemediationKindMitigation, Confidence: 40,
		ExecuteCommand: "kubectl set resources deployment leaky-app -n demo --limits=memory=1Gi", TradeOff: "buys time; the leak continues"})
	assert.NoError(t, firingPlanRefusal(context.Background(), "acct", "evt-leak-b", withMitigation))

	stubFiring(t, false)
	assert.NoError(t, firingPlanRefusal(context.Background(), "acct", "evt-leak-c", leakHardeningOnly()), "not firing: no refusal")
	assert.NoError(t, firingPlanRefusal(context.Background(), "acct", "evt-leak-d", leakSourceFixOnly()))
}

// The code fix for the worker OOM, written as an action: validation keeps it as a fix delivered in source, with its repo
// and reason, and nothing of it goes to hardening.
func TestSourceFixIsKeptAsAFix(t *testing.T) {
	raw := `{"root_cause":"command output is read unbounded into memory","summary":"Bound the output, and raise the limit meanwhile.",
	  "actions":[{"action":"Bound fetch output","kind":"fix","confidence":85,
	    "required_change":{"where":"code","target":"worker/fetch.go FetchOptions","change":"set MaxOutputSize to 16 MiB",
	      "repo":"https://github.com/example/worker","reason":"unbounded output is what fills memory"}},
	   {"action":"Raise the memory limit","kind":"mitigation","confidence":40,
	    "execute_command":"kubectl set resources deployment worker -n apps --limits=memory=2Gi","trade_off":"buys time; the output stays unbounded"}]}`
	var plan RemediationPlan
	require.NoError(t, json.Unmarshal([]byte(raw), &plan))
	normalizePlan(&plan, nil, nil)
	require.Len(t, plan.Actions, 2)
	fix := plan.Actions[0]
	assert.Equal(t, RemediationKindFix, fix.Kind, "kind stays fix")
	require.NotNil(t, fix.RequiredChange)
	assert.Equal(t, "code", fix.RequiredChange.Where)
	assert.Equal(t, "https://github.com/example/worker", fix.RequiredChange.Repo)
	assert.Equal(t, "unbounded output is what fills memory", fix.RequiredChange.Reason)
	delivery, _, _ := deliveryFor(fix, executorFacts{})
	assert.Equal(t, DeliverySource, delivery)
	assert.Empty(t, plan.OptionalHardening)
	stubFiring(t, true)
	assert.NoError(t, firingPlanRefusal(context.Background(), "acct", "evt-worker-oom", plan), "a fix and a mitigation: stored")
}

// The cards and get_remediation_plan say what a source step is, like the tab: the change, where and in which repo,
// made in source and then marked done; not "a person makes this change".
func TestSourceStepTextAgreesWithTheTab(t *testing.T) {
	code := RemediationAction{Action: "Bound fetch output", Kind: RemediationKindFix, Delivery: DeliverySource,
		RequiredChange: &RequiredChange{Where: "code", Target: "worker/fetch.go", Change: "set MaxOutputSize", Repo: "example/worker"}}
	assert.Equal(t, "Code fix in worker/fetch.go (example/worker): set MaxOutputSize", sourceChangeText(code))
	deploy := leakSourceFixOnly().Actions[0]
	assert.Equal(t, "Change in the deployment source in Deployment demo/leaky-app container command script: bound the cache to 10000 entries with LRU eviction",
		sourceChangeText(deploy))
	assert.Empty(t, sourceChangeText(RemediationAction{ExecuteCommand: "kubectl rollout restart deployment x"}))

	decision := stepDecisionText(slackCardStep{SourceChange: sourceChangeText(deploy), DeliveryReason: "change it in the repository"})
	assert.True(t, strings.HasPrefix(decision, "none yet; Change in the deployment source in Deployment demo/leaky-app"))
	assert.Contains(t, decision, "then marked done")
	assert.NotContains(t, decision, "a person makes this change")

	// The Slack thread's fix options say the same, and offer no Run for it.
	last := optionText(deploy, 0, -1)
	assert.Contains(t, last, "Change in the deployment source in Deployment demo/leaky-app")
	assert.Contains(t, last, "Make the change in source, then mark it made")
	assert.NotContains(t, last, "a person makes this change")
	assert.NotEqual(t, pickKindRun, pickMode(deploy), "no Use this fix for a source step")

	// With no repo known, applying it from the chat says to make the change and mark it done (the leaky app's plan).
	_, err := stepRequest(leakSourceFixOnly(), "a", "e", runStepRequest{PlanVersion: "v1", Step: 1, Action: "apply"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "when they have made it, use done")
}

// The rule is on the store path of a plan an agent writes: the planning turn's hardening-only plan for a firing
// alert (the leaky app's shape) is refused once, then stored when sent again.
func TestFiringPlanRefusalOnTheStorePath(t *testing.T) {
	f := newPlanningFixture(t)
	swap(t, &targetReader, func(*security.RequestContext, string, []targetSpec) []targetRead { return nil })
	swap(t, &shownTargetState, &targetStateMemo{seen: map[string]time.Time{}})
	_, err := f.db.Exec(`INSERT INTO event_log_analysis (event_id, event_fingerprint, analysis, summary, status, cloud_account_id, event_aggregation_key, analysis_type)
		VALUES ($1, 'fp-1', '{}', 'INV: the cache grows until the OOM kill', 'COMPLETED', $2, 'agg', 'investigation')`, f.event, f.account)
	require.NoError(t, err)
	raw, err := json.Marshal(leakHardeningOnly())
	require.NoError(t, err)
	store := func() error {
		_, _, err := storeNewPlanWith(f.ctx(), f.account, f.event, writtenPlan(string(raw)), 0, planStoreOptions{allowEmpty: true})
		return err
	}
	err = store()
	require.Error(t, err, "the alert fires (status FIRING)")
	assert.Contains(t, err.Error(), "still firing")
	require.NoError(t, store(), "sent again unchanged: stored")
}
