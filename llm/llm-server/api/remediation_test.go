package api

import (
	"testing"

	"nudgebee/llm/common"
	"nudgebee/llm/tools"

	"github.com/stretchr/testify/assert"
)

// TestContainsShellMetacharacters_BlocksInjection covers the exact bypass strings from the security
// review: a read-looking prefix that smuggles a second, destructive command past classification while
// the shell on the workspace pod still evaluates it.
func TestContainsShellMetacharacters_BlocksInjection(t *testing.T) {
	blocked := []string{
		"kubectl get pods; kubectl delete ns production",
		"kubectl get pods && kubectl delete deploy api -n prod",
		"kubectl get pods | xargs -I{} kubectl delete pod {}",
		"kubectl get pods $(kubectl delete ns prod)",
		"kubectl get pods `kubectl delete ns prod`",
		"kubectl get pods\nkubectl delete ns prod",
		"kubectl get pods > /etc/passwd",
		"kubectl get pods < /dev/null",
	}
	for _, cmd := range blocked {
		assert.True(t, containsShellMetacharacters(cmd), "expected metacharacter command to be rejected: %q", cmd)
	}
}

// TestContainsShellMetacharacters_AllowsPlainCommands ensures ordinary single kubectl/helm/argocd
// invocations (including patch payloads with braces and quotes) are not falsely rejected.
func TestContainsShellMetacharacters_AllowsPlainCommands(t *testing.T) {
	allowed := []string{
		"kubectl get pods -n prod",
		"kubectl rollout restart deployment api -n prod",
		"kubectl scale deployment api --replicas=3 -n prod",
		`kubectl patch deploy nginx -n prod --type=json -p=[{"op":"replace","path":"/spec/replicas","value":3}]`,
		"helm upgrade api ./chart --set image.tag=1.2.3 -n prod",
		"argocd app sync my-app",
	}
	for _, cmd := range allowed {
		assert.False(t, containsShellMetacharacters(cmd), "expected plain command to be allowed: %q", cmd)
	}
}

// TestRemediationRelayModule_Routing verifies the command is dispatched to the relay job and tool that
// carry the right credentials, case-insensitively.
func TestRemediationRelayModule_Routing(t *testing.T) {
	cases := []struct {
		command  string
		wantJob  tools.RelayJob
		wantTool string
	}{
		{"kubectl get pods -n prod", tools.RelayJobKubectl, tools.ToolExecuteKubectlCommand},
		{"KUBECTL get pods -n prod", tools.RelayJobKubectl, tools.ToolExecuteKubectlCommand},
		{"helm status api -n prod", tools.RelayJobHelm, tools.ToolExecuteHelmCommand},
		{"argocd app get my-app", tools.RelayJobArgoCD, tools.ToolExecuteArgoCDCommand},
		{"systemctl restart kubelet", tools.RelayJobShell, tools.ToolExecuteServerCommand},
	}
	for _, tc := range cases {
		job, tool := remediationRelayModule(tc.command)
		assert.Equal(t, tc.wantJob, job, "job for %q", tc.command)
		assert.Equal(t, tc.wantTool, tool, "tool for %q", tc.command)
	}
}

// TestValidateCommandSafety_BlocksCatastrophic confirms the backstop still rejects the catastrophic
// host-level patterns independent of the metacharacter and RBAC gates.
func TestValidateCommandSafety_BlocksCatastrophic(t *testing.T) {
	assert.Error(t, tools.ValidateCommandSafety("rm -rf /"))
	assert.Error(t, tools.ValidateCommandSafety("dd if=/dev/zero of=/dev/sda"))
	assert.NoError(t, tools.ValidateCommandSafety("kubectl get pods -n prod"))
}

// TestRemediationPlan_UnmarshalActions verifies the model-generated action label and confidence parse
// into the plan (leniently, tolerating markdown fences from the LLM).
func TestRemediationPlan_UnmarshalActions(t *testing.T) {
	raw := "```json\n" + `{"root_cause":"replicas scaled to zero","summary":"scale back up",` +
		`"hypotheses":[{"hypothesis":"HPA scaled the deployment to zero","reasoning":["replicas=0","HPA minReplicas is 0"],"confidence":70}],` +
		`"actions":[` +
		`{"hypothesis":"HPA scaled the deployment to zero","action":"scale","title":"Scale order-service to 1 replica","confidence":60,` +
		`"execute_command":"kubectl scale deployment order-service --replicas=1 -n ecommerce",` +
		`"verify_command":"kubectl get deployment order-service -n ecommerce",` +
		`"rollback_command":"kubectl scale deployment order-service --replicas=0 -n ecommerce"}]}` + "\n```"

	var plan RemediationPlan
	err := common.ExtractAndUnmarshalJSON([]byte(raw), &plan)
	assert.NoError(t, err)
	assert.Len(t, plan.Hypotheses, 1)
	assert.Equal(t, "HPA scaled the deployment to zero", plan.Hypotheses[0].Hypothesis)
	assert.Equal(t, StringList{"replicas=0", "HPA minReplicas is 0"}, plan.Hypotheses[0].Reasoning)
	assert.Equal(t, Confidence(70), plan.Hypotheses[0].Confidence)
	assert.Len(t, plan.Actions, 1)
	assert.Equal(t, "scale", plan.Actions[0].Action)
	assert.Equal(t, Confidence(60), plan.Actions[0].Confidence)
	assert.Equal(t, "HPA scaled the deployment to zero", plan.Actions[0].Hypothesis)
	assert.Equal(t, "Scale order-service to 1 replica", plan.Actions[0].Title)
	assert.Equal(t, "kubectl scale deployment order-service --replicas=1 -n ecommerce", plan.Actions[0].ExecuteCommand)
}

// TestConfidence_Unmarshal covers the model-shape variations the review flagged: integers, numeric
// strings, fractions (read as percentages), a bare 1 (1%, not 100%), and out-of-range clamping.
func TestConfidence_Unmarshal(t *testing.T) {
	cases := []struct {
		raw  string
		want Confidence
	}{
		{`85`, 85},
		{`"85"`, 85},
		{`0.85`, 85},
		{`1`, 1},
		{`0`, 0},
		{`100`, 100},
		{`999`, 100},
		{`-5`, 0},
		{`"abc"`, 0},
		{`null`, 0},
		{`"NaN"`, 0},
		{`"Infinity"`, 0},
		{`"-Infinity"`, 0},
	}
	for _, tc := range cases {
		var c Confidence
		err := c.UnmarshalJSON([]byte(tc.raw))
		assert.NoError(t, err, "raw=%s", tc.raw)
		assert.Equal(t, tc.want, c, "raw=%s", tc.raw)
	}
}

// TestStringList_Unmarshal covers reasoning emitted as an array, as a bullet/newline string, and as
// empty — all normalized to a clean list of points.
func TestStringList_Unmarshal(t *testing.T) {
	var arr StringList
	assert.NoError(t, arr.UnmarshalJSON([]byte(`["- point one","point two"]`)))
	assert.Equal(t, StringList{"point one", "point two"}, arr)

	var str StringList
	assert.NoError(t, str.UnmarshalJSON([]byte(`"- first\n- second\nthird"`)))
	assert.Equal(t, StringList{"first", "second", "third"}, str)

	var empty StringList
	assert.NoError(t, empty.UnmarshalJSON([]byte(`""`)))
	assert.Nil(t, empty)
}

// TestParseRelayExecResult confirms a JSON envelope with a non-zero exit_code is recognized as a
// failure (the "green success on a failed command" bug), while plain output is passed through.
func TestParseRelayExecResult(t *testing.T) {
	stdout, stderr, code, parsed := parseRelayExecResult(`{"exit_code":1,"stderr":"error: unknown command \"frobnicate\""}`)
	assert.True(t, parsed)
	assert.Equal(t, 1, code)
	assert.Equal(t, `error: unknown command "frobnicate"`, stderr)
	assert.Equal(t, "", stdout)

	stdout, _, code, parsed = parseRelayExecResult(`{"exit_code":0,"stdout":"deployment.apps/api scaled"}`)
	assert.True(t, parsed)
	assert.Equal(t, 0, code)
	assert.Equal(t, "deployment.apps/api scaled", stdout)

	// Plain (non-envelope) output: treated as success, passed through untouched.
	stdout, _, code, parsed = parseRelayExecResult("pod/foo   1/1   Running")
	assert.False(t, parsed)
	assert.Equal(t, 0, code)
	assert.Equal(t, "pod/foo   1/1   Running", stdout)
}

// TestValidateCommandSafety_WhitespaceAndVariants covers the backstop misses from the review:
// flag-order and extra-whitespace variants of the catastrophic rm.
func TestValidateCommandSafety_WhitespaceAndVariants(t *testing.T) {
	for _, cmd := range []string{"rm -fr /", "rm -rf  /", "rm -r -f /", "rm -rf --no-preserve-root /"} {
		assert.Error(t, tools.ValidateCommandSafety(cmd), "expected blocked: %q", cmd)
	}
}

// A mitigation must not be able to claim it resolves the root cause. The model routinely reports a
// restart at 90%+ confidence because it is answering "will this command succeed" rather than "will
// this stop the problem recurring", so the cap is enforced server-side.
func TestNormalizePlan_ClampsMitigationConfidence(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "Restart flagd deployment", Kind: "mitigation", Confidence: 95},
		{Action: "Raise checkout memory limit", Kind: "fix", Confidence: 88},
	}}

	normalizePlan(&plan, []string{"code_fix"})

	// Looked up by name, not index: normalizePlan also reorders fixes ahead of mitigations.
	assert.Equal(t, Confidence(maxMitigationConfidence), actionByName(t, plan, "Restart flagd deployment").Confidence,
		"mitigation confidence must be capped")
	assert.Equal(t, Confidence(88), actionByName(t, plan, "Raise checkout memory limit").Confidence,
		"a fix keeps its confidence")
}

// actionByName finds an action regardless of the order normalizePlan settled on.
func actionByName(t *testing.T, plan RemediationPlan, name string) RemediationAction {
	t.Helper()
	for _, a := range plan.Actions {
		if a.Action == name {
			return a
		}
	}
	t.Fatalf("no action named %q in plan", name)
	return RemediationAction{}
}

// Kind is free text from the model. Anything not recognizably "fix" defaults to mitigation: showing
// a mitigation as a fix misleads the operator, whereas the reverse only under-claims.
func TestNormalizePlan_DefaultsUnknownKindToMitigation(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "empty kind", Kind: "", Confidence: 90},
		{Action: "unrecognized kind", Kind: "workaround", Confidence: 90},
		{Action: "padded fix", Kind: "  FIX  ", Confidence: 90},
	}}

	normalizePlan(&plan, []string{"code_fix"})

	assert.Equal(t, RemediationKindMitigation, actionByName(t, plan, "empty kind").Kind)
	assert.Equal(t, Confidence(maxMitigationConfidence), actionByName(t, plan, "empty kind").Confidence)
	assert.Equal(t, RemediationKindMitigation, actionByName(t, plan, "unrecognized kind").Kind)
	assert.Equal(t, RemediationKindFix, actionByName(t, plan, "padded fix").Kind, "kind is matched case-insensitively after trimming")
	assert.Equal(t, Confidence(90), actionByName(t, plan, "padded fix").Confidence)
}

// A JSON payload inside a model-generated JSON reply reliably arrives truncated at the first inner
// quote. The result has no metacharacters and matches no destructive pattern, so this is the only
// guard standing between it and an opaque relay failure.
func TestIsStructurallyTruncated(t *testing.T) {
	truncated := []string{
		`kubectl patch configmap flagd-config -n demo -p '{`,
		`kubectl patch deployment postgresql -n demo -p '{"spec":{"replicas":3}`,
		`kubectl set env deployment app -n demo KEY="value`,
	}
	for _, cmd := range truncated {
		assert.True(t, isStructurallyTruncated(cmd), "expected truncated command to be rejected: %q", cmd)
	}

	wellFormed := []string{
		`kubectl rollout restart deployment flagd -n demo`,
		`kubectl set resources deployment postgresql -n demo --limits=memory=512Mi`,
		`kubectl patch deployment app -n demo --type merge -p '{"spec":{"replicas":3}}'`,
		`kubectl get pods -n demo -o jsonpath={.items[0].metadata.name}`,
	}
	for _, cmd := range wellFormed {
		assert.False(t, isStructurallyTruncated(cmd), "expected well-formed command to be allowed: %q", cmd)
	}
}

// An action cannot be more certain than the hypothesis it addresses — a 100% action under a 95%
// hypothesis is the contradiction operators see on the card.
func TestNormalizePlan_ClampsActionToHypothesisConfidence(t *testing.T) {
	plan := RemediationPlan{
		Hypotheses: []RemediationHypothesis{{Hypothesis: "Non-atomic write in flagd-ui", Confidence: 95}},
		Actions: []RemediationAction{
			{Action: "exact match", Hypothesis: "Non-atomic write in flagd-ui", Kind: "fix", Confidence: 100},
			{Action: "case and space insensitive match", Hypothesis: "  non-atomic write in FLAGD-UI  ", Kind: "fix", Confidence: 80},
			{Action: "unmatched hypothesis", Hypothesis: "a hypothesis that was never listed", Kind: "fix", Confidence: 100},
		},
	}

	normalizePlan(&plan, []string{"code_fix"})

	assert.Equal(t, Confidence(95), actionByName(t, plan, "exact match").Confidence, "action is capped at its hypothesis")
	assert.Equal(t, Confidence(80), actionByName(t, plan, "case and space insensitive match").Confidence, "an action below the ceiling is untouched")
	assert.Equal(t, Confidence(100), actionByName(t, plan, "unmatched hypothesis").Confidence, "an unmatched hypothesis leaves the value alone")
}

// The mitigation cap and the hypothesis ceiling compose: whichever binds harder wins.
func TestNormalizePlan_MitigationCapAndHypothesisCeilingCompose(t *testing.T) {
	plan := RemediationPlan{
		Hypotheses: []RemediationHypothesis{{Hypothesis: "cause", Confidence: 30}},
		Actions: []RemediationAction{
			{Hypothesis: "cause", Kind: "mitigation", Confidence: 90},
		},
	}

	normalizePlan(&plan, []string{"code_fix"})

	assert.Equal(t, Confidence(30), plan.Actions[0].Confidence, "the 30% hypothesis binds harder than the 50% mitigation cap")
}

// An action with no command is applied on another surface. When no surface holds anything for this
// event, such an action is a dead end — it tells the operator a fix exists and points at an empty
// panel — so it must not survive. This is the shape that shipped a "refactor scraper.py" card for an
// event that had no code analysis at all.
func TestNormalizePlan_DropsCommandlessActionWithoutArtifact(t *testing.T) {
	newPlan := func() RemediationPlan {
		return RemediationPlan{Actions: []RemediationAction{
			{Action: "Refactor a source file", Kind: "fix", Confidence: 40},
			{Action: "Raise memory limit", Kind: "mitigation", Confidence: 50, ExecuteCommand: "kubectl set resources deployment app -n ns --limits=memory=1Gi"},
		}}
	}

	withoutArtifact := newPlan()
	normalizePlan(&withoutArtifact, nil)
	assert.Len(t, withoutArtifact.Actions, 1, "the command-less action has nothing backing it")
	assert.Equal(t, "Raise memory limit", withoutArtifact.Actions[0].Action)

	withArtifact := newPlan()
	normalizePlan(&withArtifact, []string{"code_fix"})
	assert.Len(t, withArtifact.Actions, 2, "with an artifact present the action is real and is kept")

	blank := newPlan()
	normalizePlan(&blank, []string{"", "   "})
	assert.Len(t, blank.Actions, 1, "blank entries do not count as artifacts")
}

// Verify and rollback describe checking or undoing a command that ran. A command-less action runs
// nothing, so those would report on unrelated state — as one live plan did, "verifying" a code change
// by tailing logs.
func TestNormalizePlan_StripsVerifyAndRollbackFromCommandlessAction(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "Apply a code change", Kind: "fix", Confidence: 80,
			VerifyCommand: "kubectl logs -l app=x -n ns --tail=100", RollbackCommand: "kubectl rollout undo deployment x -n ns"},
	}}

	normalizePlan(&plan, []string{"code_fix"})

	assert.Empty(t, plan.Actions[0].VerifyCommand)
	assert.Empty(t, plan.Actions[0].RollbackCommand)
}

// Removing the cause outranks restoring service, however reliable the latter is. The prompt asks for
// this and the model sorts by confidence instead, so the server decides the order.
func TestNormalizePlan_OrdersFixesBeforeMitigations(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "restart", Kind: "mitigation", Confidence: 50, ExecuteCommand: "kubectl rollout restart deployment a -n ns"},
		{Action: "low-confidence fix", Kind: "fix", Confidence: 40, ExecuteCommand: "kubectl set resources deployment a -n ns --limits=memory=1Gi"},
		{Action: "high-confidence fix", Kind: "fix", Confidence: 90, ExecuteCommand: "kubectl set image deployment a c=i:2 -n ns"},
	}}

	normalizePlan(&plan, nil)

	assert.Equal(t, []string{"high-confidence fix", "low-confidence fix", "restart"},
		[]string{plan.Actions[0].Action, plan.Actions[1].Action, plan.Actions[2].Action},
		"fixes first (best first), mitigations after — even a 40%% fix outranks a 50%% mitigation")
}
