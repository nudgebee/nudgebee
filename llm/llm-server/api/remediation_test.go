package api

import (
	"errors"
	"fmt"
	"nudgebee/llm/workspace"
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

// TestRemediationSubstrate_Routing verifies the executor is chosen from the ACCOUNT'S PROVIDER and
// then narrowed by the command, rather than from the command's first word alone. The old
// prefix-only dispatch sent kubectl from an AWS account down a relay to an agent that cannot exist
// there, and dropped GCP's `bq` into an uncredentialed shell.
func TestRemediationSubstrate_Routing(t *testing.T) {
	cases := []struct {
		name       string
		provider   string
		command    string
		wantCloud  string
		wantJob    tools.RelayJob
		wantTool   string
		wantReject bool
	}{
		// A Kubernetes account is not kubectl-only -- helm and argocd are ordinary remediations on one.
		{name: "k8s kubectl", provider: "K8s", command: "kubectl get pods -n prod", wantJob: tools.RelayJobKubectl, wantTool: tools.ToolExecuteKubectlCommand},
		{name: "k8s kubectl uppercase", provider: "K8s", command: "KUBECTL get pods -n prod", wantJob: tools.RelayJobKubectl, wantTool: tools.ToolExecuteKubectlCommand},
		{name: "k8s helm", provider: "K8s", command: "helm status api -n prod", wantJob: tools.RelayJobHelm, wantTool: tools.ToolExecuteHelmCommand},
		{name: "k8s argocd", provider: "K8s", command: "argocd app get my-app", wantJob: tools.RelayJobArgoCD, wantTool: tools.ToolExecuteArgoCDCommand},
		{name: "k8s shell", provider: "K8s", command: "systemctl restart kubelet", wantJob: tools.RelayJobShell, wantTool: tools.ToolExecuteServerCommand},

		// Each cloud provider reaches its own CLI, and only its own.
		{name: "aws cli on aws", provider: "AWS", command: "aws ec2 describe-instances", wantCloud: tools.ToolExecuteAwsCliCommand},
		{name: "azure cli on azure", provider: "Azure", command: "az vm list", wantCloud: tools.ToolExecuteAzureCliCommand},
		{name: "gcloud on gcp", provider: "GCP", command: "gcloud compute instances list", wantCloud: tools.ToolExecuteGcpCliCommand},
		{name: "gsutil on gcp", provider: "GCP", command: "gsutil ls gs://bucket", wantCloud: tools.ToolExecuteGcpCliCommand},
		// bq is a GCP CLI the prefix table never listed, so it used to run uncredentialed and exit 0.
		{name: "bq on gcp", provider: "GCP", command: "bq query --nouse_legacy_sql SELECT 1", wantCloud: tools.ToolExecuteGcpCliCommand},

		// Cross-provider commands are refused with a reason, not dispatched somewhere that fails opaquely.
		{name: "kubectl on aws", provider: "AWS", command: "kubectl get pods", wantReject: true},
		{name: "helm on gcp", provider: "GCP", command: "helm upgrade api ./chart", wantReject: true},
		{name: "aws cli on azure", provider: "Azure", command: "aws s3 ls", wantReject: true},
		{name: "gcloud on k8s", provider: "K8s", command: "gcloud compute instances list", wantReject: true},

		// A shell command still has somewhere to go on a cloud account: its workspace pod.
		{name: "shell on aws", provider: "AWS", command: "df -h", wantJob: tools.RelayJobShell, wantTool: tools.ToolExecuteServerCommand},

		// An unreadable or unmodelled provider must not take remediation offline -- fall back to the
		// old command-shaped dispatch.
		{name: "unknown provider keeps cloud dispatch", provider: "", command: "aws ec2 describe-instances", wantCloud: tools.ToolExecuteAwsCliCommand},
		// Splitting before lowercasing means every whitespace form still yields the binary.
		{name: "tab separated", provider: "AWS", command: "aws\tec2 describe-instances", wantCloud: tools.ToolExecuteAwsCliCommand},
		{name: "leading whitespace", provider: "GCP", command: "   gcloud compute instances list", wantCloud: tools.ToolExecuteGcpCliCommand},
		{name: "empty command falls back to shell", provider: "AWS", command: "   ", wantJob: tools.RelayJobShell, wantTool: tools.ToolExecuteServerCommand},
		{name: "cloudfoundry falls back to shell", provider: "CloudFoundry", command: "cf apps", wantJob: tools.RelayJobShell, wantTool: tools.ToolExecuteServerCommand},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tools.RemediationSubstrateFor(tc.provider, tc.command)
			if tc.wantReject {
				assert.NotEmpty(t, got.Reject, "expected a refusal naming the account's provider")
				assert.Empty(t, got.CloudCliTool)
				assert.Empty(t, got.RelayTool)
				return
			}
			assert.Empty(t, got.Reject)
			assert.Equal(t, tc.wantCloud, got.CloudCliTool, "cloud tool for %q", tc.command)
			assert.Equal(t, tc.wantTool, got.RelayTool, "relay tool for %q", tc.command)
			if tc.wantTool != "" {
				assert.Equal(t, tc.wantJob, got.RelayJob, "relay job for %q", tc.command)
			}
		})
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

// A failed execute must still be recorded. Gating the resolution on success meant "nothing was
// tried here" and "three things were tried and all failed" looked identical in the resolutions
// list, which is the opposite of what an operator needs.
func TestShouldPersistRemediationResolution_RecordsFailuresAndOnlyExecute(t *testing.T) {
	for _, tc := range []struct {
		name    string
		eventId string
		slot    string
		want    bool
	}{
		{"execute slot", "evt-1", RemediationSlotExecute, true},
		{"empty slot defaults to execute", "evt-1", "", true},
		{"slot casing and padding ignored", "evt-1", "  EXECUTE  ", true},
		// A verify observes and a rollback reverses; neither resolves the event.
		{"verify is not a resolution", "evt-1", "verify", false},
		{"rollback is not a resolution", "evt-1", "rollback", false},
		// Ad-hoc runs outside an event have nothing to attach to.
		{"no event id", "", RemediationSlotExecute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldPersistRemediationResolution(tc.eventId, tc.slot); got != tc.want {
				t.Errorf("shouldPersistRemediationResolution(%q, %q) = %v; want %v", tc.eventId, tc.slot, got, tc.want)
			}
		})
	}
}

// A verify asserts something about observed state. "Exited 0" is not the same as "confirmed the fix",
// and collapsing the two is how an operator is told a problem is solved when nothing was checked.
func TestVerificationPassed(t *testing.T) {
	result := func(success bool, stdout string) tools.RemediationExecutionResult {
		return tools.RemediationExecutionResult{Success: success, Stdout: stdout}
	}

	t.Run("observed something and succeeded", func(t *testing.T) {
		if got := verificationPassed(result(true, "deployment \"web\" successfully rolled out"), true); got != true {
			t.Errorf("got %v; want true", got)
		}
	})

	t.Run("ran and failed", func(t *testing.T) {
		if got := verificationPassed(result(false, ""), true); got != false {
			t.Errorf("got %v; want false", got)
		}
	})

	// The case this exists for: exit 0 having observed nothing proves nothing.
	t.Run("exited 0 observing nothing is not a pass", func(t *testing.T) {
		for _, empty := range []string{"", "   ", "\n\t "} {
			if got := verificationPassed(result(true, empty), true); got != nil {
				t.Errorf("stdout %q: got %v; want nil", empty, got)
			}
		}
	})

	// The executor merged stdout/stderr and discarded the exit code, so there is nothing to judge by
	// — even when output came back.
	t.Run("no exit code reported is not a pass", func(t *testing.T) {
		if got := verificationPassed(result(true, "some output"), false); got != nil {
			t.Errorf("got %v; want nil", got)
		}
	})
}

// A verify must never file a resolution of its own — that counted one remediation attempt three
// times. It annotates the execute attempt instead.
func TestVerifySlotDoesNotCreateItsOwnResolution(t *testing.T) {
	if shouldPersistRemediationResolution("evt-1", RemediationSlotVerify) {
		t.Error("verify must not create a resolution")
	}
	if !isVerifySlot(RemediationSlotVerify) || !isVerifySlot("  VERIFY  ") {
		t.Error("isVerifySlot must ignore casing and padding, as the slot gate does")
	}
	if isVerifySlot(RemediationSlotExecute) || isVerifySlot("rollback") {
		t.Error("only verify is a verify")
	}
}

// TestRemediationCloudCliTool_Routing verifies which commands leave the relay path for a workspace
// pod. Only the cloud CLIs do: everything else targets a host inside the customer network and stays
// on the relay, so a "" result here is what keeps kubectl reaching the cluster agent.
func TestRemediationCloudCliTool_Routing(t *testing.T) {
	cases := []struct {
		command string
		want    string
	}{
		{"aws ec2 describe-instances --region us-east-1", tools.ToolExecuteAwsCliCommand},
		{"AWS s3 ls", tools.ToolExecuteAwsCliCommand},
		{"az vm list", tools.ToolExecuteAzureCliCommand},
		{"gcloud compute instances list", tools.ToolExecuteGcpCliCommand},
		{"gsutil ls gs://bucket", tools.ToolExecuteGcpCliCommand},
		{"kubectl get pods -n prod", ""},
		{"helm status api -n prod", ""},
		{"systemctl restart kubelet", ""},
		// A command whose name merely starts with a CLI name is not that CLI.
		{"awslogs get mygroup", ""},
		{"", ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tools.CloudCliToolFor(tc.command), "routing for %q", tc.command)
	}
}

// TestCloudCliMetacharacterGuard_AllowsQuotedJmesPath is the reason the guard is quote-aware for cloud
// commands: JMESPath uses ( ) | [ ] inside --query routinely, and the unmodified guard rejected every
// one of them. The check runs on the quote-stripped command, so the operators stay allowed only while
// they remain inside quotes.
func TestCloudCliMetacharacterGuard_AllowsQuotedJmesPath(t *testing.T) {
	allowed := []string{
		`aws ec2 describe-instances --query "Reservations[].Instances[?State.Name=='running'].InstanceId"`,
		`aws logs filter-log-events --filter-pattern "ERROR" --query "events[*].message | [0:5]"`,
		`az vm list --query "[?powerState=='VM running'].name"`,
		`gcloud compute instances list --filter="status=(RUNNING)"`,
	}
	for _, cmd := range allowed {
		assert.NotEmpty(t, tools.CloudCliToolFor(cmd), "precondition: %q must route to a cloud CLI", cmd)
		assert.False(t, isStructurallyTruncated(cmd), "precondition: %q must have balanced quotes", cmd)
		assert.False(t, containsShellMetacharacters(tools.StripQuotedContent(cmd)),
			"expected quoted JMESPath to be allowed: %q", cmd)
	}
}

// TestCloudCliMetacharacterGuard_StillBlocksUnquotedInjection is the other half: quote-awareness must
// not become an injection hole. An operator outside quotes can still start a second command, so it is
// still rejected — and an attempt to hide one behind an unbalanced quote is caught by the truncation
// check that runs first.
func TestCloudCliMetacharacterGuard_StillBlocksUnquotedInjection(t *testing.T) {
	blocked := []string{
		"aws s3 ls; aws s3 rb --force s3://prod-backups",
		"aws s3 ls && aws iam delete-user --user-name admin",
		"aws s3 ls $(aws iam create-access-key --user-name admin)",
		"az vm list | xargs -I{} az vm delete --name {}",
	}
	for _, cmd := range blocked {
		assert.True(t, containsShellMetacharacters(tools.StripQuotedContent(cmd)),
			"expected unquoted injection to be rejected: %q", cmd)
	}

	// Hiding a metacharacter behind an unbalanced quote defeats the stripper, which is exactly why
	// isStructurallyTruncated is checked before it rather than after.
	sneaky := `aws s3 ls "; aws s3 rb --force s3://prod-backups`
	assert.False(t, containsShellMetacharacters(tools.StripQuotedContent(sneaky)),
		"precondition: the stripper does swallow this, so the truncation check must be what rejects it")
	assert.True(t, isStructurallyTruncated(sneaky), "unbalanced quote must be rejected")
}

// A double-quoted span is not inert. The workspace runs the command under `sh -c`, where $ and a
// backtick still begin a substitution inside double quotes, so stripping such a span wholesale let a
// substitution reach the shell with every guard reporting the command clean. Single quotes really do
// suppress expansion, and the operators that are literal inside double quotes must stay allowed --
// that is what keeps a quoted JMESPath filter usable.
func TestCloudGuardRejectsSubstitutionInsideDoubleQuotes(t *testing.T) {
	rejected := []struct{ name, command string }{
		{"command substitution in double quotes", `aws s3 ls "$(id)"`},
		{"backtick substitution in double quotes", "aws ec2 describe-instances --filters \"`id`\""},
		{"variable expansion in double quotes", `aws s3 ls "$HOME"`},
		{"substitution unquoted", `aws s3 ls $(id)`},
		{"chaining unquoted", `aws s3 ls; id`},
	}
	for _, tc := range rejected {
		t.Run("rejected/"+tc.name, func(t *testing.T) {
			assert.True(t, containsShellMetacharacters(tools.StripQuotedContentForShellCheck(tc.command)),
				"a live substitution must remain visible to the guard")
		})
	}

	allowed := []struct{ name, command string }{
		{"jmespath filter keeps working", `aws ec2 describe-instances --query "Reservations[].Instances[?State.Name=='running']"`},
		{"operators are literal inside double quotes", `aws s3 ls "a;b&c|d<e>f"`},
		{"single quotes suppress substitution", `aws s3 ls '$(id)'`},
		{"escaped dollar is a literal dollar", `aws s3 ls "\$HOME"`},
		{"plain command", `aws ec2 describe-instance-status --instance-ids i-0abc`},
	}
	for _, tc := range allowed {
		t.Run("allowed/"+tc.name, func(t *testing.T) {
			assert.False(t, containsShellMetacharacters(tools.StripQuotedContentForShellCheck(tc.command)),
				"a legitimate cloud CLI command must still pass")
		})
	}
}

// workspaceOutcome is where the cloud path's success, exit code and "did the executor tell us"
// decision actually live, so it is tested directly rather than by re-asserting errors.Is on an error
// built in the test.
//
// The trap it guards: ErrWorkspaceCommandFailed does NOT mean "ran and exited non-zero".
// classifyExecuteResponse raises it for any command_status:"failed", which the workspace agent also
// uses for pre-execution rejections — an empty command, a bad workspace path, the security validator
// refusing an absolute path (reachable from a real command: `aws s3 cp s3://b/k /var/tmp/x`).
// Nothing ran in those cases, so claiming a verified exit code 1 for them states a specific wrong
// number where the old caption was merely vague.
func TestWorkspaceOutcome(t *testing.T) {
	wsErr := func(stderr string) error {
		return fmt.Errorf("cloud cli: aws_execute failed: %w",
			&workspace.CommandFailure{Status: "failed", StdErr: stderr})
	}

	for _, tc := range []struct {
		name         string
		execErr      error
		raw          string
		wantStdout   string
		wantStderr   string
		wantExitCode int
		wantSuccess  bool
		wantReported bool
	}{
		{
			name: "success is definitive", execErr: nil, raw: "i-0abc	running",
			wantStdout: "i-0abc	running", wantExitCode: 0, wantSuccess: true, wantReported: true,
		},
		{
			name: "a real non-zero exit carries its own code", execErr: wsErr("exit status 254"),
			wantStderr: "exit status 254", wantExitCode: 254, wantReported: true,
		},
		{
			name: "exit status 1 is still a real run", execErr: wsErr("exit status 1"),
			wantStderr: "exit status 1", wantExitCode: 1, wantReported: true,
		},
		{
			// The command never reached cmd.Run(), so its exit code is not ours to state.
			name: "security rejection never ran", execErr: wsErr("Security validation failed: absolute path"),
			wantStderr: "Security validation failed: absolute path", wantExitCode: 1, wantReported: false,
		},
		{
			name: "empty command never ran", execErr: wsErr("Command is empty"),
			wantStderr: "Command is empty", wantExitCode: 1, wantReported: false,
		},
		{
			name: "transport failure leaves everything unknown", execErr: errors.New("connection refused"),
			wantStderr: "connection refused", wantExitCode: 1, wantReported: false,
		},
		{
			// A CommandFailure with no message must not blank the operator's stderr pane.
			name: "empty workspace message falls back to the chain", execErr: wsErr(""),
			wantStderr:   "cloud cli: aws_execute failed: workspace command failed: status=\"failed\" error=\"\"",
			wantExitCode: 1, wantReported: false,
		},
		{
			// error_hint is written to steer the model; the operator wants what their command printed.
			name: "recovery envelope is unwrapped", execErr: wsErr("exit status 255"),
			raw:        `{"error_hint":"read the error before switching commands","original_error":"An error occurred (UnauthorizedOperation)"}`,
			wantStdout: "An error occurred (UnauthorizedOperation)",
			wantStderr: "exit status 255", wantExitCode: 255, wantReported: true,
		},
		{
			// A success is returned unexamined -- large describe-* payloads are never parsed.
			name: "plain json output is not mistaken for an envelope", execErr: nil,
			raw:        `{"Reservations":[]}`,
			wantStdout: `{"Reservations":[]}`, wantExitCode: 0, wantSuccess: true, wantReported: true,
		},
		{
			// Even a success whose payload happens to carry the envelope's keys is left alone, since
			// only a failure can be one.
			name: "success carrying envelope-like keys is left alone", execErr: nil,
			raw:        `{"error_hint":"h","original_error":"e"}`,
			wantStdout: `{"error_hint":"h","original_error":"e"}`, wantExitCode: 0, wantSuccess: true, wantReported: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, exitCode, success, reported := workspaceOutcome(tc.execErr, tc.raw)
			assert.Equal(t, tc.wantStdout, stdout, "stdout")
			assert.Equal(t, tc.wantStderr, stderr, "stderr")
			assert.Equal(t, tc.wantExitCode, exitCode, "exit code")
			assert.Equal(t, tc.wantSuccess, success, "success")
			assert.Equal(t, tc.wantReported, reported, "exitCodeReported")
		})
	}
}

// describeRemediationAccount states the facts a runnable command needs and the investigation prose
// does not reliably carry. Both are optional, and neither known must leave the context untouched
// rather than prepending an empty heading.
func TestDescribeRemediationAccountShape(t *testing.T) {
	// The DB is not available in unit tests, so provider/region both resolve empty here. That is the
	// case worth pinning: a lookup failure must degrade the prompt, never fail generation.
	assert.Equal(t, "", describeRemediationAccount("", ""),
		"nothing known must produce no heading at all")
	assert.Equal(t, "", describeRemediationAccount("883efbbc-bb2c-404b-9ed9-6b7ecbf6f509", "1653f230-5e49-4351-b1f0-9b2bf5d72475"),
		"an unreachable metastore must degrade silently, not panic or emit a half-filled heading")
}

// subject_node holds the REGION on a cloud event and a real node name on a Kubernetes one, so the
// provider decides whether it may be presented as a region at all.
func TestEventRegionOnlyForCloudAccounts(t *testing.T) {
	const acct, evt = "883efbbc-bb2c-404b-9ed9-6b7ecbf6f509", "1653f230-5e49-4351-b1f0-9b2bf5d72475"

	assert.Equal(t, "", eventRegion("K8s", acct, evt),
		"a Kubernetes node name is not a region and must never be offered as one")
	assert.Equal(t, "", eventRegion("k8s", acct, evt), "the provider match is case-insensitive")
	assert.Equal(t, "", eventRegion("", acct, evt), "an unknown provider must not be treated as cloud")
	assert.Equal(t, "", eventRegion("AWS", "", evt), "no account, no lookup")
	assert.Equal(t, "", eventRegion("AWS", acct, ""), "no event, no lookup")
}
