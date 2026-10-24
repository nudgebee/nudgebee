package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/audit"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
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

// The remediation audit keeps the command but not the credential in it.
func TestRemediationAuditMasksCredentials(t *testing.T) {
	ctx := security.NewRequestContextForTenantAccountAdmin("audit-tenant", "audit-user", []string{"account"})
	for _, tc := range auditedCommandShapes {
		t.Run(tc.name, func(t *testing.T) {
			a := remediationAudit(ctx, "account", tc.command, "kubectl", true, audit.EventActionCreate, "remediation-panel").Audits[0]
			stored, err := json.Marshal(a)
			require.NoError(t, err)
			require.NotContains(t, string(stored), "Qv7rTx2mWp9z")
			command := a.EventAttr["command"].(string)
			require.Contains(t, command, "[REDACTED:")
			require.Contains(t, command, tc.keep)
			require.Equal(t, map[string]any{"command": command}, a.EventState)
		})
	}
	plain := remediationAudit(ctx, "account", "kubectl rollout restart deployment api -n prod", "kubectl", true, audit.EventActionCreate, "remediation-panel").Audits[0]
	require.Equal(t, "kubectl rollout restart deployment api -n prod", plain.EventAttr["command"])
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

	// Seen on a read-only agent: the command never ran, so it must not read as success.
	stdout, stderr, code, parsed = parseRelayExecResult(`{"error":"kubectl: verb \"set\" not in read-only allowlist; enable runner.enableWritePermissions for writes, or route mutating actions through pkg/mutate"}`)
	assert.True(t, parsed)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "read-only allowlist")

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

	normalizePlan(&plan, []string{"code_fix"}, nil)

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

	normalizePlan(&plan, []string{"code_fix"}, nil)

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

	normalizePlan(&plan, []string{"code_fix"}, nil)

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

	normalizePlan(&plan, []string{"code_fix"}, nil)

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
	normalizePlan(&withoutArtifact, nil, nil)
	assert.Len(t, withoutArtifact.Actions, 1, "the command-less action has nothing backing it")
	assert.Equal(t, "Raise memory limit", withoutArtifact.Actions[0].Action)

	withArtifact := newPlan()
	normalizePlan(&withArtifact, []string{"code_fix"}, nil)
	assert.Len(t, withArtifact.Actions, 2, "with an artifact present the action is real and is kept")

	blank := newPlan()
	normalizePlan(&blank, []string{"", "   "}, nil)
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

	normalizePlan(&plan, []string{"code_fix"}, nil)

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

	normalizePlan(&plan, nil, nil)

	assert.Equal(t, []string{"high-confidence fix", "low-confidence fix", "restart"},
		[]string{plan.Actions[0].Action, plan.Actions[1].Action, plan.Actions[2].Action},
		"fixes first (best first), mitigations after — even a 40%% fix outranks a 50%% mitigation")
}

// Execute and rollback are steps a person approves with Run, and each is claimed as a run row before it
// is sent. Verify and the outcome check are reads that annotate a run, never runs of their own.
func TestIsRunSlot(t *testing.T) {
	for slot, want := range map[string]bool{
		RemediationSlotExecute: true,
		"":                     true, // an empty slot is execute
		"  EXECUTE  ":          true,
		"rollback":             true,
		"verify":               false,
		"outcome":              false,
		"manual":               false,
	} {
		assert.Equal(t, want, isRunSlot(slot), slot)
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
	if isRunSlot(RemediationSlotVerify) {
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

// A plan is stored per alert, so a new firing's lookup can land on the plan an earlier firing
// generated. Only a plan saved for the requested event may be restored; anything else makes the
// panel present another incident's root cause and commands as this one's.
func TestPlanSavedForEvent_OnlyRestoresThisIncidentsPlan(t *testing.T) {
	const thisEvent = "f6a775cf-cad9-4a56-a1d2-3559f5db955b"

	assert.True(t, planSavedForEvent(&events.EventAnalysis{RelatedEventId: thisEvent}, thisEvent))
	assert.True(t, planSavedForEvent(&events.EventAnalysis{RelatedEventId: strings.ToUpper(thisEvent)}, thisEvent),
		"uuid case must not decide ownership")
	assert.False(t, planSavedForEvent(&events.EventAnalysis{RelatedEventId: "a3adb998-a45c-44fe-8a20-246c2fd63e10"}, thisEvent),
		"a plan generated for an earlier firing of the same alert")
	assert.False(t, planSavedForEvent(&events.EventAnalysis{}, thisEvent),
		"a row with no event id belongs to no incident")
}

// A plan restored after its investigation was re-run describes the previous analysis.
func TestPlanOlderThanAnalysis(t *testing.T) {
	saved := &events.EventAnalysis{UpdatedAt: time.Date(2026, 9, 19, 7, 7, 0, 0, time.UTC)}
	completed := string(events.AnalysisStatusCompleted)
	rerun := &events.EventAnalysis{Status: completed, UpdatedAt: time.Date(2026, 9, 19, 10, 20, 0, 0, time.UTC)}
	earlier := &events.EventAnalysis{Status: completed, UpdatedAt: time.Date(2026, 9, 19, 7, 4, 0, 0, time.UTC)}
	running := &events.EventAnalysis{Status: string(events.AnalysisStatusInProgress), UpdatedAt: rerun.UpdatedAt}

	assert.True(t, planOlderThanAnalysis(saved, earlier, rerun), "investigation re-run after the plan")
	assert.False(t, planOlderThanAnalysis(saved, earlier), "plan built from the current analysis")
	assert.False(t, planOlderThanAnalysis(saved, running, nil), "a run still in progress keeps the plan until it completes")
}

// D01 on dev: the restart that makes a ConfigMap change take effect was ranked as a separate low
// mitigation, so the "fix" was applied alone and the pod stayed unready. After keeps it behind its change.
func TestKeepDependentsAfter(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "Restart web", Kind: RemediationKindMitigation, Confidence: 25, After: "Patch web config", ExecuteCommand: "kubectl rollout restart deployment web -n demo"},
		{Action: "Raise web limit", Kind: RemediationKindMitigation, Confidence: 40, ExecuteCommand: "kubectl set resources deployment web -n demo --limits=memory=512Mi"},
		{Action: "Patch web config", Kind: RemediationKindFix, Confidence: 90, ExecuteCommand: "kubectl patch configmap web -n demo -p '{\"data\":{\"A\":\"b\"}}'"},
	}}
	notes := normalizePlan(&plan, nil, nil)
	names := []string{}
	for _, a := range plan.Actions {
		names = append(names, a.Action)
	}
	assert.Equal(t, []string{"Patch web config", "Restart web", "Raise web limit"}, names)
	assert.Equal(t, "Patch web config", plan.Actions[1].After)
	for _, n := range notes {
		assert.NotEqual(t, "after_cleared", n.Rule)
	}
}

// An After that names nothing kept, or loops, is cleared rather than hiding the action.
func TestKeepDependentsAfterClearsBrokenReferences(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "A", Kind: RemediationKindFix, Confidence: 90, After: "B"},
		{Action: "B", Kind: RemediationKindFix, Confidence: 80, After: "A"},
		{Action: "C", Kind: RemediationKindFix, Confidence: 70, After: "no such action"},
		{Action: "D", Kind: RemediationKindFix, Confidence: 60, After: "D"},
	}}
	notes := keepDependentsAfter(&plan)
	assert.Len(t, plan.Actions, 4)
	cleared := 0
	for _, n := range notes {
		if n.Rule == "after_cleared" {
			cleared++
		}
	}
	assert.Equal(t, 3, cleared, "one end of the A/B loop, C and D")
	assert.Empty(t, plan.Actions[2].After)
}

// An action that follows a loop is not itself in it and keeps its order; a differently cased name
// is stored as the parent's exact name, which is what the card matches.
func TestKeepDependentsAfterClearsOnlyTheLoop(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "A", After: "b"},
		{Action: "B", After: "C"},
		{Action: "C", After: "B"},
	}}
	keepDependentsAfter(&plan)
	after := map[string]string{}
	for _, a := range plan.Actions {
		after[a.Action] = a.After
	}
	assert.Equal(t, "B", after["A"])
	assert.True(t, (after["B"] == "") != (after["C"] == ""), "exactly one end of the B/C loop is cleared")
}

// A required change applied by the operator can precede a command (D12: fix the Secret, then restart).
func TestAfterMayFollowARequiredChange(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "Restart api", Kind: RemediationKindFix, Confidence: 85, After: "Add API_TOKEN key", ExecuteCommand: "kubectl rollout restart deployment api -n demo"},
		{Action: "Add API_TOKEN key", Kind: RemediationKindFix, Confidence: 80, RequiredChange: &RequiredChange{Where: "runtime", Target: "Secret demo/api", Change: "add key API_TOKEN"}},
	}}
	normalizePlan(&plan, nil, nil)
	assert.Equal(t, "Add API_TOKEN key", plan.Actions[0].Action)
	assert.Equal(t, "Restart api", plan.Actions[1].Action)
}

// workspaceOutcome is tested directly: ErrWorkspaceCommandFailed does NOT mean "ran and exited non-zero". The
// workspace agent also returns command_status:"failed" for pre-execution rejections (empty command, bad path, the
// validator refusing `aws s3 cp s3://b/k /var/tmp/x`): nothing ran, so a verified exit code 1 would be wrong.
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

// The tests below need a disposable PostgreSQL database with the app migrations applied, named by
// REMEDIATION_RUN_TEST_DB_URL (e.g. postgres://postgres@127.0.0.1:55432/run_once?sslmode=disable);
// without it they are skipped.

// remediationScratchDB is an isolated schema named prefix_<uuid> on that database with ddl run in it,
// dropped when the test ends.
func remediationScratchDB(t *testing.T, prefix, ddl string) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("REMEDIATION_RUN_TEST_DB_URL")
	if dsn == "" {
		t.Skip("set REMEDIATION_RUN_TEST_DB_URL to a disposable PostgreSQL database with the app migrations applied")
	}
	admin, err := sqlx.Connect("postgres", dsn)
	require.NoError(t, err)
	schema := prefix + "_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sqlx.Connect("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		_ = admin.Close()
	})
	_, err = db.Exec(ddl)
	require.NoError(t, err)
	return db
}

// withMetastore installs db as the metastore for one test.
func withMetastore(t *testing.T, db *sqlx.DB) {
	t.Helper()
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: db}, nil
	})
	t.Cleanup(func() { common.RegisterDatabaseManagerHook(common.Metastore, nil) })
}

// swap sets *p to v for one test.
// The restore waits for the thread posts the test left running in the background (inThreadBackground): they may
// still read what was swapped, and the next test swaps it again.
func swap[T any](t *testing.T, p *T, v T) {
	t.Helper()
	prev := *p
	*p = v
	t.Cleanup(func() {
		threadPostsInFlight.Wait()
		*p = prev
	})
}

func errorMessage(out map[string]any) string {
	errs, _ := out["errors"].([]any)
	if len(errs) == 0 {
		return ""
	}
	m, _ := errs[0].(map[string]any)
	s, _ := m["message"].(string)
	return s
}

func dataOf(out map[string]any) map[string]any {
	d, _ := out["data"].(map[string]any)
	return d
}

// --- caller identity ---

// A decision is for the person in the caller's headers; the body may not name anyone else.
func TestDecisionCallerRejection(t *testing.T) {
	call := func(headers map[string]string, sv, payload map[string]any) int {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/", nil)
		for k, v := range headers {
			c.Request.Header.Set(k, v)
		}
		code, _ := decisionCallerRejection(c, &ActionRequest{SessionVariables: sv}, payload)
		return code
	}
	person := map[string]string{"x-tenant-id": "t1", "x-user-id": "u1"}
	with := func(k, v string) map[string]string {
		h := map[string]string{}
		for a, b := range person {
			h[a] = b
		}
		if v == "" {
			delete(h, k)
		} else {
			h[k] = v
		}
		return h
	}
	assert.Zero(t, call(person, map[string]any{"user_id": "u1", "tenant_id": "t1"}, map[string]any{"account_id": "a"}), "headers and matching session variables")

	for name, tc := range map[string]struct {
		headers     map[string]string
		sv, payload map[string]any
		want        int
	}{
		"no user header":                     {with("x-user-id", ""), nil, nil, 401},
		"nil user header":                    {with("x-user-id", "00000000-0000-0000-0000-000000000000"), nil, nil, 401},
		"no tenant header":                   {with("x-tenant-id", ""), nil, nil, 401},
		"identity only in the body":          {with("x-user-id", ""), map[string]any{"user_id": "u1"}, nil, 401},
		"session names another person":       {person, map[string]any{"user_id": "u2"}, nil, 400},
		"session names another tenant":       {person, map[string]any{"tenant_id": "t2"}, nil, 400},
		"request input names another person": {person, nil, map[string]any{"user_id": "u2"}, 400},
		"request input names another tenant": {person, nil, map[string]any{"tenant_id": "t2"}, 400},
	} {
		assert.Equal(t, tc.want, call(tc.headers, tc.sv, tc.payload), name)
	}
}

// The handler refuses a client-chosen configuration, and a decision without the plan version, before it reads anything.
func TestExecuteRefusesBeforeReadingAnything(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for field, tc := range map[string]struct {
		value any
		want  string
	}{
		"config_name":  {"other-cluster", "config_name is not accepted"},
		"plan_version": {"", "plan_version is required"},
	} {
		input := map[string]any{"account_id": "a1", "event_id": "e1", "command": runCommand, "slot": "execute", "plan_version": "v", field: tc.value}
		body, _ := json.Marshal(map[string]any{"action": map[string]any{"name": "ai_remediation_execute"}, "input": input})
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/v1/analyze/remediation/execute", bytes.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Set("x-tenant-id", "t1")
		c.Request.Header.Set("x-user-id", "u1")
		processRemediationExecute(c, tracenoop.NewTracerProvider().Tracer("test"), metricnoop.NewMeterProvider().Meter("test"))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		assert.Equal(t, 400, rec.Code, field)
		assert.Contains(t, errorMessage(out), tc.want, field)
	}
}

// --- run: pure helpers ---

// A step's identity is the action's place in the plan version, whichever surface decides.
func TestPlanStep(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "Update the secret", RequiredChange: &RequiredChange{Target: "Secret a", Change: "rotate"}},
		{Action: "Restart", ExecuteCommand: runCommand, RollbackCommand: "kubectl rollout undo deployment/api -n shop"},
	}}
	for _, tc := range []struct{ slot, command, action, want string }{
		{RemediationSlotExecute, runCommand, "", "1:apply"},
		{"", " " + runCommand + " ", "", "1:apply"},
		{RemediationSlotManual, runCommand, "Restart", "1:apply"},
		{RemediationSlotRollback, "kubectl rollout undo deployment/api -n shop", "", "1:rollback"},
		{RemediationSlotManual, "", "Update the secret", "0:apply"},
		{RemediationSlotExecute, "kubectl delete ns shop", "", ""},
	} {
		assert.Equal(t, tc.want, planStep(plan, tc.slot, tc.command, tc.action), tc)
	}
}

// Only a run the agent reported as exit 0 is a verified success.
func TestVerifiedSuccess(t *testing.T) {
	assert.True(t, verifiedSuccess("Success", runFacts{ExitCodeReported: true}))
	assert.False(t, verifiedSuccess("Success", runFacts{}), "no exit code")
	assert.False(t, verifiedSuccess("Success", runFacts{ExitCodeReported: true, Manual: true}), "marked done by a person")
	assert.False(t, verifiedSuccess("Unknown", runFacts{ExitCodeReported: true}))
	assert.False(t, verifiedSuccess("InProgress", runFacts{}))
}

// Whether the agent never started a command is read from its reply status alone, never from wording.
func TestAgentRefusedBeforeRunningReadsTheStatusOnly(t *testing.T) {
	for code, want := range map[int]bool{400: true, 401: true, 404: true, 503: true, 500: false, 504: false, 502: false, 409: false} {
		assert.Equal(t, want, agentRefusedBeforeRunning(&tools.RelayCommandRefusedError{StatusCode: code, Message: "kubectl: verb \"set\" not in read-only allowlist"}), code)
	}
}

// The Unknown-outcome acknowledgement never claims the earlier command stopped, and states the reaper wait.
func TestUnknownOutcomeMessageDoesNotClaimTheCommandStopped(t *testing.T) {
	for _, c := range []*events.DecisionConflict{
		{Reason: events.DecisionUnknownOutcome, WaitSeconds: 125},
		{Reason: events.DecisionUnknownOutcome},
	} {
		msg := decisionConflictMessage(c)
		assert.Contains(t, msg, "may have run, and its effects may still be in progress")
		assert.NotContains(t, strings.ToLower(msg), "stopped")
	}
	assert.Contains(t, decisionConflictMessage(&events.DecisionConflict{Reason: events.DecisionUnknownOutcome, WaitSeconds: 125}), "in 3 min")
	assert.Contains(t, decisionConflictMessage(&events.DecisionConflict{Reason: events.DecisionAlreadyDecided, Status: "Success", Manual: true}), "marked done")
}

// Verify and the outcome check run unconfirmed, so only a kubectl strict read or a cloud read off the deny-list runs.
func TestCheckRejection(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	for _, tc := range []struct {
		provider, cmd string
		allowed       bool
	}{
		{"k8s", "kubectl rollout status deployment/api -n shop --timeout=90s", true},
		{"k8s", "kubectl get pods -n shop -l app=api", true},
		{"aws", "aws cloudwatch describe-alarms --alarm-names cpu-high", true},
		{"k8s", "kubectl rollout restart deployment/api -n shop", false},
		{"k8s", "kubectl get pods -n shop --context prod", false},
		{"k8s", "helm status api -n shop", false},
		{"aws", "aws ec2 terminate-instances --instance-ids i-1", false},
		{"azure", "aws cloudwatch describe-alarms", false},
		// The AWS rules class these as reads; an unconfirmed check must not run them.
		{"aws", "aws secretsmanager get-secret-value --secret-id db", false},
		{"aws", "aws sts assume-role --role-arn arn:aws:iam::1:role/x --role-session-name s", false},
		{"aws", "aws eks update-kubeconfig --name prod", false},
		{"aws", "aws cloudwatch describe-alarms | tee /tmp/x", false},
	} {
		got := checkRejection(ctx, "check-rejection", tc.provider, tc.cmd)
		if tc.allowed {
			assert.Empty(t, got, tc.cmd)
		} else {
			assert.Contains(t, got, "Check it yourself", tc.cmd)
		}
	}
}

// The planner sees a declined step as declined with its reason, and a run with what its follow-up found.
func TestRenderAttempt(t *testing.T) {
	declined := renderAttempt(attempt{Kind: "CommandExecution", Ref: runCommand, Status: "Declined",
		Data: []byte(`{"reason":"not during the sale"}`), At: time.Now(), SameEvent: true})
	assert.Contains(t, declined, "a person declined "+runCommand)
	assert.Contains(t, declined, `(reason: "not during the sale")`)
	assert.NotContains(t, declined, "ran ")

	data := []byte(`{"followup":{"findings":"still OOMKilled at 512Mi","summary":"ignored when findings exist"}}`)
	line := renderAttempt(attempt{Kind: "CommandExecution", Ref: "kubectl rollout restart deploy/api", Status: "Success", Data: data, At: time.Now(), SameEvent: true})
	assert.Contains(t, line, `NudgeBee's follow-up check after it found: "still OOMKilled at 512Mi"`)
	assert.NotContains(t, renderAttempt(attempt{Kind: "CommandExecution", Ref: "x", Status: "Success", Data: []byte(`{}`), At: time.Now()}), "follow-up")
}

// --- run: against the scratch database and a fake relay ---

type runFixture struct {
	t       *testing.T
	db      *sqlx.DB
	account string
	event   string
	relay   *runRelay
	stored  storedPlan // the plan as the person reviewed it
}

type runRelay struct {
	mu        sync.Mutex
	requests  int
	body      map[string]any
	rowAtSend [2]string // the newest run row's status and approver when the request arrived
	runId     string    // the newest run row when the request arrived
	respond   func(w http.ResponseWriter, r *http.Request)
}

const (
	runCommand = "kubectl rollout restart deployment/api -n shop"
	okReply    = `{"status_code":200,"data":{"stdout":"ok","stderr":"","exit_code":0}}`
	lostReply  = `{"status_code":502,"action":"error","data":"agent connection lost"}`
)

func runPlan() RemediationPlan {
	return RemediationPlan{Actions: []RemediationAction{{Action: "Restart", ExecuteCommand: runCommand, RollbackCommand: "kubectl rollout undo deployment/api -n shop"}}}
}

// newRunFixture is a Kubernetes account with a connected agent, one event with a stored plan, and a fake relay.
func newRunFixture(t *testing.T) *runFixture {
	t.Helper()
	db := remediationScratchDB(t, "run_api", `CREATE TABLE event_resolution (LIKE public.event_resolution INCLUDING ALL);
		CREATE TABLE event_log_analysis (LIKE public.event_log_analysis INCLUDING ALL);
		CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid NOT NULL);
		CREATE TABLE cloud_accounts (id uuid PRIMARY KEY, cloud_provider text, account_name text, tenant uuid);
		CREATE TABLE agent (cloud_account_id uuid, type text, status text)`)
	f := &runFixture{t: t, db: db, account: uuid.NewString(), event: uuid.NewString(), relay: &runRelay{}}
	for _, insert := range []string{`INSERT INTO cloud_accounts VALUES ($1, 'k8s', 'shop-prod', 'aaaaaaaa-0000-4000-8000-000000000001')`, `INSERT INTO agent VALUES ($1, 'k8s', 'CONNECTED')`} {
		_, err := db.Exec(insert, f.account)
		require.NoError(t, err)
	}
	_, err := db.Exec(`INSERT INTO events VALUES ($1, $2)`, f.event, f.account)
	require.NoError(t, err)
	f.stored = storedPlan{Plan: runPlan(), RowId: uuid.NewString()}
	f.regenerate("plan 1")
	withMetastore(t, db)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.relay.mu.Lock()
		f.relay.requests++
		f.relay.body = body
		var row struct {
			Id     string `db:"id"`
			Status string `db:"status"`
			User   string `db:"resolver_id"`
		}
		if db.Get(&row, `SELECT id::text AS id, status, resolver_id FROM event_resolution WHERE event_id = $1 ORDER BY created_at DESC LIMIT 1`, f.event) == nil {
			f.relay.rowAtSend, f.relay.runId = [2]string{row.Status, row.User}, row.Id
		}
		respond := f.relay.respond
		f.relay.mu.Unlock()
		respond(w, r)
	}))
	t.Cleanup(server.Close)
	swap(t, &config.Config.RelayServerEndpoint, server.URL)
	return f
}

// regenerate stores text as the event's plan and makes it the plan the person reviewed.
func (f *runFixture) regenerate(text string) {
	f.t.Helper()
	_, err := f.db.Exec(`INSERT INTO event_log_analysis (id, event_id, analysis_type, analysis) VALUES ($1, $2, 'remediation', $3)
		ON CONFLICT (id) DO UPDATE SET analysis = EXCLUDED.analysis`, f.stored.RowId, f.event, text)
	require.NoError(f.t, err)
	f.stored.Version = events.PlanVersion(text)
}

// regenerateBehindTheReviewer changes the stored plan while f.stored stays the one the person reviewed.
func (f *runFixture) regenerateBehindTheReviewer() {
	reviewed := f.stored
	f.regenerate("plan 2")
	f.stored = reviewed
}

func agentReplies(body string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// run presses Run on command as user-1, on goCtx, and returns the HTTP status and body.
func (f *runFixture) run(goCtx context.Context, command string, opts ...func(*RemediationExecuteRequest)) (int, map[string]any) {
	f.t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	request := RemediationExecuteRequest{AccountId: f.account, EventId: f.event, Command: command, Slot: RemediationSlotExecute}
	for _, o := range opts {
		o(&request)
	}
	ctx := security.NewRequestContextForTenantAdminWithUser(goCtx, "aaaaaaaa-0000-4000-8000-000000000001", "user-1")
	request.PlanVersion = f.stored.Version
	runRemediationStep(ctx, request, f.stored, command, "user-1").write(c)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// decideStep records a decision that runs nothing ("decline" or "manual") on the plan's first action as user-1.
func (f *runFixture) decideStep(slot, reason string) (int, map[string]any) {
	f.t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	request := RemediationExecuteRequest{AccountId: f.account, EventId: f.event, Command: runCommand, Slot: slot, Reason: reason, PlanVersion: f.stored.Version}
	ctx := security.NewRequestContextForTenantAdminWithUser(context.Background(), "tenant", "user-1")
	if slot == RemediationSlotDecline {
		recordDecline(ctx, request, "user-1", f.stored).write(c)
	} else {
		recordOperatorStep(ctx, request, "user-1", f.stored).write(c)
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// rows is the event's run rows, oldest first: each row's data with its status.
func (f *runFixture) rows() []map[string]any {
	f.t.Helper()
	var rows []struct {
		Status string `db:"status"`
		Data   []byte `db:"data"`
	}
	require.NoError(f.t, f.db.Select(&rows, `SELECT status, data FROM event_resolution WHERE event_id = $1 ORDER BY created_at`, f.event))
	out := []map[string]any{}
	for _, r := range rows {
		m := map[string]any{}
		_ = json.Unmarshal(r.Data, &m)
		m["status"] = r.Status
		out = append(out, m)
	}
	return out
}

// A run is sent once and settled from what came back: only the agent's own pre-dispatch statuses prove
// "not sent"; an error without an exit code is Unknown; a clean reply without one is an unverified success.
func TestRunSettlesFromWhatCameBack(t *testing.T) {
	cases := []struct {
		name     string
		respond  func(http.ResponseWriter, *http.Request)
		status   string
		notSent  bool
		reported bool
	}{
		{"exit 0", agentReplies(`{"status_code":200,"data":{"stdout":"restarted","stderr":"","exit_code":0}}`), "Success", false, true},
		{"exit 1", agentReplies(`{"status_code":200,"data":{"stdout":"","stderr":"Error from server (NotFound)","exit_code":1}}`), "Failed", false, true},
		{"reply without an exit code", agentReplies(`{"status_code":200,"data":{"response":"deployment.apps/api restarted"}}`), "Success", false, false},
		{"agent malformed request", agentReplies(`{"status_code":400,"data":{"error":"malformed request envelope"}}`), "Failed", true, false},
		{"agent rejected the request's auth", agentReplies(`{"status_code":401,"data":{"error":"signature invalid"}}`), "Failed", true, false},
		{"agent has no such action", agentReplies(`{"status_code":404,"data":{"error":"action not registered: kubectl_command_executor"}}`), "Failed", true, false},
		{"agent overloaded", agentReplies(`{"status_code":503,"data":{"error":"agent overloaded"}}`), "Failed", true, false},
		{"agent 500 worded as a read-only allowlist refusal", agentReplies(`{"status_code":500,"data":{"error":"kubectl: verb \"rollout\" not in read-only allowlist; set enableWritePermissions"}}`), "Unknown", false, false},
		{"agent error after starting", agentReplies(`{"status_code":500,"data":{"error":"context canceled"}}`), "Unknown", false, false},
		{"agent connection lost", agentReplies(lostReply), "Unknown", false, false},
		{"task expired", agentReplies(`{"status_code":504,"data":{"error":"task timeout"}}`), "Unknown", false, false},
		{"connection dropped after the write", func(w http.ResponseWriter, _ *http.Request) {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
		}, "Unknown", false, false},
		// The relay's own error status cannot be told from a proxy's in front of it.
		{"relay error status", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"errors":[{"code":503,"message":"agent not connected"}]}`))
		}, "Unknown", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRunFixture(t)
			f.relay.respond = tc.respond
			code, out := f.run(context.Background(), runCommand)
			require.Equal(t, http.StatusOK, code)
			data := dataOf(out)
			assert.Equal(t, tc.status, data["status"])
			assert.Equal(t, tc.status == "Success", data["success"])
			assert.Equal(t, tc.reported, data["exit_code"] != nil, "an exit code is shown only when the agent reported one")
			assert.Equal(t, 1, f.relay.requests, "sent exactly once")
			rows := f.rows()
			require.Len(t, rows, 1)
			assert.Equal(t, tc.status, rows[0]["status"])
			assert.Equal(t, tc.notSent, rows[0]["not_sent"] == true)
			assert.Equal(t, tc.reported, rows[0]["exit_code_reported"] == true)
		})
	}
}

// The run row exists, InProgress and naming the approver, before the request reaches the relay, and it
// binds the plan version, the step and the exact command.
func TestRunIsRecordedBeforeItIsSent(t *testing.T) {
	f := newRunFixture(t)
	f.relay.respond = agentReplies(okReply)
	code, _ := f.run(context.Background(), runCommand)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, [2]string{"InProgress", "user-1"}, f.relay.rowAtSend)
	body := f.relay.body["body"].(map[string]any)
	assert.Equal(t, "kubectl_command_executor", body["action_name"])
	assert.Equal(t, runCommand, body["action_params"].(map[string]any)["command"])
	row := f.rows()[0]
	assert.Equal(t, "0:apply", row["step"])
	assert.Equal(t, f.stored.Version, row["plan_version"])
	assert.Equal(t, events.ApprovalKey(f.account, f.event, f.stored.Version, "0:apply", runCommand), row["approval_key"])
	assert.Equal(t, map[string]any{"account_id": f.account, "account_name": "shop-prod", "provider": "k8s"}, row["target"])
}

// Closing the page cancels the request's context; a claimed run is still sent once and settled.
func TestClosingThePageDoesNotCancelARun(t *testing.T) {
	for name, beforeSend := range map[string]bool{"closed before the send": true, "closed while the agent works": false} {
		t.Run(name, func(t *testing.T) {
			f := newRunFixture(t)
			goCtx, cancel := context.WithCancel(context.Background())
			if beforeSend {
				cancel()
			}
			f.relay.respond = func(w http.ResponseWriter, r *http.Request) {
				cancel()
				agentReplies(okReply)(w, r)
			}
			_, _ = f.run(goCtx, runCommand)
			assert.Equal(t, 1, f.relay.requests)
			assert.Equal(t, "Success", f.rows()[0]["status"])
		})
	}
}

// A decided step is not sent again, even with the Unknown acknowledgement.
func TestReplayedRunIsNotSentAgain(t *testing.T) {
	f := newRunFixture(t)
	f.relay.respond = agentReplies(okReply)
	code, _ := f.run(context.Background(), runCommand)
	require.Equal(t, http.StatusOK, code)
	code, out := f.run(context.Background(), runCommand)
	assert.Equal(t, http.StatusConflict, code)
	assert.Contains(t, errorMessage(out), "already decided")
	code, _ = f.run(context.Background(), runCommand, func(r *RemediationExecuteRequest) { r.AcknowledgeUnknown = true })
	assert.Equal(t, http.StatusConflict, code)
	assert.Equal(t, 1, f.relay.requests)
	assert.Len(t, f.rows(), 1)
}

// A helm, argocd or shell step runs through the pod script executor, recorded as done but not verified.
func TestNonKubectlStepRunsThroughThePodScriptExecutor(t *testing.T) {
	f := newRunFixture(t)
	helm := "helm rollback api 3 -n shop"
	f.stored.Plan.Actions = append(f.stored.Plan.Actions, RemediationAction{Action: "Roll back the release", ExecuteCommand: helm})
	f.relay.respond = agentReplies(`{"status_code":200,"data":{"response":"Rollback was a success!"}}`)
	code, out := f.run(context.Background(), helm)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "pod_script_run_enricher", f.relay.body["body"].(map[string]any)["action_name"])
	assert.Equal(t, "Success", dataOf(out)["status"])
	row := f.rows()[0]
	assert.Equal(t, "1:apply", row["step"])
	assert.Equal(t, false, row["exit_code_reported"], "done, not verified")
}

// A command this account's executor cannot run, or one aimed at another cluster or identity, is refused
// before anything is recorded or sent.
func TestRunRefusesForeignCommands(t *testing.T) {
	f := newRunFixture(t)
	f.relay.respond = agentReplies(okReply)
	for cmd, want := range map[string]string{
		"aws ec2 reboot-instances --instance-ids i-1":                    "",
		"kubectl --context other rollout restart deployment/api -n shop": "",
		runCommand + " --context prod-eu":                                "another cluster or identity",
		runCommand + " --kubeconfig=/tmp/other":                          "another cluster or identity",
		runCommand + " --as system:admin":                                "another cluster or identity",
	} {
		code, out := f.run(context.Background(), cmd)
		assert.Equal(t, http.StatusBadRequest, code, cmd)
		assert.Contains(t, errorMessage(out), want, cmd)
	}
	assert.Zero(t, f.relay.requests)
	assert.Empty(t, f.rows())
}

// A command runs only on the event's own, confirmed account.
func TestRunOnlyOnTheEventsOwnAccount(t *testing.T) {
	t.Run("an account that cannot be confirmed", func(t *testing.T) {
		f := newRunFixture(t)
		_, err := f.db.Exec(`DELETE FROM cloud_accounts WHERE id = $1`, f.account)
		require.NoError(t, err)
		code, out := f.run(context.Background(), runCommand)
		assert.Equal(t, http.StatusServiceUnavailable, code)
		assert.Contains(t, errorMessage(out), "could not be confirmed, so nothing was run")
		assert.Zero(t, f.relay.requests)
		assert.Empty(t, f.rows())
	})
	t.Run("an account the event does not belong to", func(t *testing.T) {
		f := newRunFixture(t)
		other := uuid.NewString()
		_, err := f.db.Exec(`INSERT INTO cloud_accounts VALUES ($1, 'k8s', 'billing-prod')`, other)
		require.NoError(t, err)
		code, _ := f.run(context.Background(), runCommand, func(r *RemediationExecuteRequest) { r.AccountId = other })
		assert.Equal(t, http.StatusConflict, code)
		assert.Zero(t, f.relay.requests)
		assert.Empty(t, f.rows())
	})
}

// When nothing could reach the agent the run is "not sent"; after an unreachable relay the step is retried.
func TestNotSentRunStaysOpen(t *testing.T) {
	for name, cut := range map[string]func(t *testing.T, f *runFixture) (restore func()){
		"relay unreachable": func(t *testing.T, f *runFixture) func() {
			relayURL := config.Config.RelayServerEndpoint
			l, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			config.Config.RelayServerEndpoint = "http://" + l.Addr().String()
			require.NoError(t, l.Close())
			return func() { config.Config.RelayServerEndpoint = relayURL }
		},
		"agent disconnected": func(t *testing.T, f *runFixture) func() {
			_, err := f.db.Exec(`UPDATE agent SET status = 'DISCONNECTED'`)
			require.NoError(t, err)
			return nil // the agent's status is cached, so no retry here
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRunFixture(t)
			f.relay.respond = agentReplies(okReply)
			restore := cut(t, f)
			_, out := f.run(context.Background(), runCommand)
			assert.Equal(t, "Failed", dataOf(out)["status"])
			assert.Equal(t, true, f.rows()[0]["not_sent"])
			assert.Zero(t, f.relay.requests)

			if restore == nil {
				return
			}
			restore()
			code, out := f.run(context.Background(), runCommand)
			require.Equal(t, http.StatusOK, code)
			assert.Equal(t, "Success", dataOf(out)["status"])
			assert.Equal(t, 1, f.relay.requests)
			assert.Len(t, f.rows(), 2)
		})
	}
}

// A relay that never answers cannot hold a run: the send gives up at its deadline and records Unknown.
func TestRunIsBoundedWhenTheRelayStalls(t *testing.T) {
	f := newRunFixture(t)
	swap(t, &runSendTimeout, 300*time.Millisecond)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f.relay.respond = func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}
	start := time.Now()
	code, out := f.run(context.Background(), runCommand)
	require.Equal(t, http.StatusOK, code)
	assert.Less(t, time.Since(start), 5*time.Second)
	assert.Equal(t, "Unknown", dataOf(out)["status"])
	assert.Equal(t, "Unknown", f.rows()[0]["status"])
	assert.Equal(t, 1, f.relay.requests)
}

// A settle that cannot be confirmed (row locked, or already reaped) claims no outcome or record, and a late
// settle never overwrites what the reaper records.
func TestRunReportsAnUnconfirmedSettleHonestly(t *testing.T) {
	t.Run("settle times out on a locked row", func(t *testing.T) {
		f := newRunFixture(t)
		swap(t, &decisionDBTimeout, 500*time.Millisecond)
		var holder *sqlx.Tx
		t.Cleanup(func() {
			if holder != nil {
				_ = holder.Rollback()
			}
		})
		f.relay.respond = func(w http.ResponseWriter, r *http.Request) {
			var err error
			holder, err = f.db.Beginx()
			require.NoError(t, err)
			_, err = holder.Exec(`SELECT id FROM event_resolution WHERE id = $1 FOR UPDATE`, f.relay.runId)
			require.NoError(t, err)
			agentReplies(`{"status_code":200,"data":{"stdout":"restarted","stderr":"","exit_code":0}}`)(w, r)
		}
		start := time.Now()
		code, out := f.run(context.Background(), runCommand)
		require.Equal(t, http.StatusOK, code)
		assert.Less(t, time.Since(start), 5*time.Second)
		data := dataOf(out)
		assert.Equal(t, "Unknown", data["status"])
		assert.Equal(t, false, data["success"])
		assert.Equal(t, runNotSettledMessage, data["error"])
		assert.Contains(t, data["error"], "do not run this step again until its recorded state is reconciled")
		assert.NotContains(t, data["error"], "recorded as")
		assert.EqualValues(t, 0, data["exit_code"], "what was observed is still shown")
		require.NoError(t, holder.Rollback())
		holder = nil
		assert.Equal(t, "InProgress", f.rows()[0]["status"], "left for the reaper")
	})
	t.Run("row already reaped", func(t *testing.T) {
		f := newRunFixture(t)
		f.relay.respond = func(w http.ResponseWriter, r *http.Request) {
			_, err := f.db.Exec(`UPDATE event_resolution SET status = 'Unknown' WHERE id = $1`, f.relay.runId)
			require.NoError(t, err)
			agentReplies(`{"status_code":200,"data":{"stdout":"restarted","stderr":"","exit_code":0}}`)(w, r)
		}
		code, out := f.run(context.Background(), runCommand)
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, "Unknown", dataOf(out)["status"])
		assert.Equal(t, runNotSettledMessage, dataOf(out)["error"])
		assert.Equal(t, "Unknown", f.rows()[0]["status"], "the earlier writer's record stands")
	})
}

// After an Unknown outcome a step (here a rollback) re-runs only acknowledged and after the reaper wait.
func TestUnknownRollbackIsRerunOnlyAcknowledgedAndAfterTheWait(t *testing.T) {
	f := newRunFixture(t)
	undo := "kubectl rollout undo deployment/api -n shop"
	asRollback := func(r *RemediationExecuteRequest) { r.Slot = RemediationSlotRollback }
	ack := func(r *RemediationExecuteRequest) { r.AcknowledgeUnknown = true }
	f.relay.respond = agentReplies(lostReply)
	_, out := f.run(context.Background(), undo, asRollback)
	require.Equal(t, "Unknown", dataOf(out)["status"])
	assert.Equal(t, "0:rollback", f.rows()[0]["step"])

	f.relay.respond = agentReplies(`{"status_code":200,"data":{"stdout":"rolled back","stderr":"","exit_code":0}}`)
	code, _ := f.run(context.Background(), undo, asRollback)
	assert.Equal(t, http.StatusConflict, code, "no acknowledgement")
	code, out = f.run(context.Background(), undo, asRollback, ack)
	assert.Equal(t, http.StatusConflict, code, "acknowledged, but before the wait")
	assert.Contains(t, errorMessage(out), "you can run it again in")
	assert.Equal(t, 1, f.relay.requests)

	_, err := f.db.Exec(`UPDATE event_resolution SET created_at = created_at - make_interval(secs => $2) WHERE event_id = $1`, f.event, events.UnknownRerunWait.Seconds()+60)
	require.NoError(t, err)
	code, out = f.run(context.Background(), undo, asRollback, ack)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "Success", dataOf(out)["status"])
	assert.Equal(t, 2, f.relay.requests)
	assert.Len(t, f.rows(), 2)
}

// An agent error worded as a refusal is kept for the reader but is Unknown, so a plain retry is refused.
func TestAgentErrorWordedAsARefusalIsNotRetryable(t *testing.T) {
	f := newRunFixture(t)
	f.relay.respond = agentReplies(`{"status_code":500,"data":{"error":"kubectl: verb \"rollout\" not in read-only allowlist"}}`)
	_, out := f.run(context.Background(), runCommand)
	data := dataOf(out)
	assert.Equal(t, "Unknown", data["status"])
	assert.Contains(t, data["error"], "may have run")
	assert.Contains(t, data["error"], "not in read-only allowlist")
	assert.Nil(t, f.rows()[0]["not_sent"])

	code, out := f.run(context.Background(), runCommand)
	assert.Equal(t, http.StatusConflict, code)
	assert.Contains(t, errorMessage(out), "unknown outcome")
	assert.Equal(t, 1, f.relay.requests)
}

// --- decisions: approval binds the reviewed plan; Run, Decline and "I ran this" are one decision ---

// A Run or Decline confirmed on a plan that was since regenerated is refused before anything is recorded or sent.
func TestDecisionOnAStalePlanIsRefused(t *testing.T) {
	for name, decide := range map[string]func(f *runFixture) (int, map[string]any){
		"run":     func(f *runFixture) (int, map[string]any) { return f.run(context.Background(), runCommand) },
		"decline": func(f *runFixture) (int, map[string]any) { return f.decideStep(RemediationSlotDecline, "") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newRunFixture(t)
			f.relay.respond = agentReplies(okReply)
			f.regenerateBehindTheReviewer()
			code, out := decide(f)
			assert.Equal(t, http.StatusConflict, code)
			assert.Equal(t, planChangedMessage, errorMessage(out))
			assert.Zero(t, f.relay.requests)
			assert.Empty(t, f.rows())
		})
	}
}

// Whichever decision on a step comes first stands; a declined step needs a new proposal.
func TestDeclineAndRunAreOneDecision(t *testing.T) {
	t.Run("decline, then run or mark done", func(t *testing.T) {
		f := newRunFixture(t)
		f.relay.respond = agentReplies(okReply)
		code, out := f.decideStep(RemediationSlotDecline, "  restarting checkout during the sale is too risky  ")
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, "Declined", dataOf(out)["status"])
		row := f.rows()[0]
		assert.Equal(t, "restarting checkout during the sale is too risky", row["reason"])
		assert.Equal(t, "0:apply", row["step"])

		code, out = f.run(context.Background(), runCommand)
		assert.Equal(t, http.StatusConflict, code)
		assert.Contains(t, errorMessage(out), "it was declined")
		code, _ = f.decideStep(RemediationSlotManual, "")
		assert.Equal(t, http.StatusConflict, code)
		assert.Zero(t, f.relay.requests)
		assert.Len(t, f.rows(), 1)
	})
	t.Run("run, then decline", func(t *testing.T) {
		f := newRunFixture(t)
		f.relay.respond = agentReplies(okReply)
		code, _ := f.run(context.Background(), runCommand)
		require.Equal(t, http.StatusOK, code)
		code, out := f.decideStep(RemediationSlotDecline, "")
		assert.Equal(t, http.StatusConflict, code)
		assert.Contains(t, errorMessage(out), "it ran")
		assert.Len(t, f.rows(), 1)
	})
	t.Run("a new proposal is a new decision", func(t *testing.T) {
		f := newRunFixture(t)
		f.relay.respond = agentReplies(okReply)
		code, _ := f.decideStep(RemediationSlotDecline, "")
		require.Equal(t, http.StatusOK, code)
		f.regenerate("plan 2")
		code, _ = f.run(context.Background(), runCommand)
		assert.Equal(t, http.StatusOK, code)
		assert.Equal(t, 1, f.relay.requests)
	})
	t.Run("a long reason in any script is kept whole", func(t *testing.T) {
		f := newRunFixture(t)
		code, _ := f.decideStep(RemediationSlotDecline, strings.Repeat("日", 600))
		require.Equal(t, http.StatusOK, code)
		assert.Equal(t, strings.Repeat("日", maxDeclineReason), f.rows()[0]["reason"])
	})
	t.Run("a step whose run has an unknown outcome is not declined", func(t *testing.T) {
		f := newRunFixture(t)
		f.relay.respond = agentReplies(lostReply)
		_, out := f.run(context.Background(), runCommand)
		require.Equal(t, "Unknown", dataOf(out)["status"])
		code, out := f.decideStep(RemediationSlotDecline, "")
		assert.Equal(t, http.StatusConflict, code)
		assert.Contains(t, errorMessage(out), "unknown outcome")
		assert.Len(t, f.rows(), 1)
	})
}

// --- chat: run_step routing ---

// A run_step call becomes the request a button sends, only with the named step's own command.
func TestStepRequest(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "Raise limit", ExecuteCommand: "kubectl set resources deploy/api -n shop --limits=memory=1Gi", RollbackCommand: "kubectl rollout undo deploy/api -n shop"},
		{Action: "Rotate the secret", RequiredChange: &RequiredChange{Target: "Secret api", Change: "rotate"}},
	}}
	req := func(step int, action, command string) (RemediationExecuteRequest, error) {
		return stepRequest(plan, "acc", "ev", runStepRequest{PlanVersion: " v1 ", Step: step, Action: action, Command: command, Reason: "too risky", Checked: true})
	}

	got, err := req(1, "apply", plan.Actions[0].ExecuteCommand)
	require.NoError(t, err)
	assert.Equal(t, RemediationExecuteRequest{AccountId: "acc", EventId: "ev", PlanVersion: "v1", Source: chatSource, AcknowledgeUnknown: true,
		Slot: RemediationSlotExecute, Command: plan.Actions[0].ExecuteCommand}, got)
	got, err = req(1, "rollback", plan.Actions[0].RollbackCommand)
	require.NoError(t, err)
	assert.Equal(t, RemediationSlotRollback, got.Slot)
	assert.Equal(t, plan.Actions[0].RollbackCommand, got.Command)
	got, err = req(1, "decline", "")
	require.NoError(t, err)
	assert.Equal(t, RemediationSlotDecline, got.Slot)
	assert.Equal(t, "too risky", got.Reason)
	assert.Equal(t, plan.Actions[0].ExecuteCommand, got.Command, "a decision names its step by its command")
	got, err = req(2, "done", "")
	require.NoError(t, err)
	assert.Equal(t, RemediationSlotManual, got.Slot)
	assert.Equal(t, "Rotate the secret", got.Action, "a step with no command is named by its action")

	for _, c := range []struct {
		step            int
		action, command string
		want            string
	}{
		{1, "apply", "kubectl delete ns shop", "not step 1's command"},
		{1, "rollback", plan.Actions[0].ExecuteCommand, "not step 1's command"},
		{2, "apply", "", "a change a person makes"},
		{2, "rollback", "", "no undo command"},
		{3, "apply", "", "steps 1 to 2"},
		{1, "run", "", "apply, rollback, decline or done"},
	} {
		_, err := req(c.step, c.action, c.command)
		assert.ErrorContains(t, err, c.want, c)
	}
}

// run_step acts only for a person with write access, and never in a read-only run, before anything is read.
func TestRunStepFromChatRefusesBeforeReading(t *testing.T) {
	const account = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	call := func(ctx *security.RequestContext) error {
		_, err := runStepFromChat(toolcore.NbToolContext{Ctx: ctx, AccountId: account}, runStepRequest{Step: 1, Action: "apply"})
		return err
	}
	writer := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{account})
	assert.ErrorContains(t, call(core.WithReadOnlyRun(writer)), "only reads")
	assert.ErrorContains(t, call(security.NewRequestContextForSuperAdmin()), "only a person")
	elsewhere := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{"dddddddd-dddd-dddd-dddd-dddddddddddd"})
	assert.ErrorContains(t, call(elsewhere), "no write access")
}

// renderPlanCards gives the model what run_step needs: the plan version, each step's command and undo.
func TestRenderPlanCards(t *testing.T) {
	at := time.Date(2026, 10, 19, 5, 0, 0, 0, time.UTC)
	text := renderPlanCards(slackCards{Cards: []slackCard{
		{Kind: "plan", PlanVersion: "v9", RootCause: "12Mi limit", Summary: "raise memory", Steps: []slackCardStep{
			{Index: 0, Title: "Raise memory", Kind: "fix", Confidence: 90, Command: "kubectl set resources ...", Rollback: "kubectl rollout undo ...", State: "Unknown", At: &at},
			{Index: 1, Title: "Restart", Confidence: 60, AfterStep: 1},
			{Index: 2, Title: "Fix the chart", DeliveryReason: "change it in the repository"},
		}},
		{Kind: "followup", Command: "kubectl set image ...", Outcome: "proposed", Summary: "OOMKilled"},
	}})
	for _, want := range []string{
		"plan_version v9",
		"Cause: 12Mi limit",
		"Step 1: Raise memory (fix, 90% likely to resolve)",
		"Undo: kubectl rollout undo ...",
		"Decision: ran, outcome unknown: it may have taken effect at 2026-10-19 05:00 UTC",
		"Runs after step 1",
		"a person makes this change outside NudgeBee (change it in the repository)",
		"Follow-up on kubectl set image ...: proposed",
	} {
		assert.Contains(t, text, want)
	}
	assert.Contains(t, renderPlanCards(slackCards{}), "no current remediation plan")
}

// A Slack step message names its step by index; the plan's own action is used, never the click's.
func TestPlanAction(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{{Action: "Patch"}, {Action: "Restart", ExecuteCommand: "kubectl rollout restart deploy/x"}}}
	action, err := planAction(plan, 1)
	require.NoError(t, err)
	assert.Equal(t, "kubectl rollout restart deploy/x", action.ExecuteCommand)
	for _, bad := range []int{-1, 2} {
		_, err := planAction(plan, bad)
		assert.ErrorContains(t, err, "steps 1 to 2", bad)
	}
}

// The follow-up's report is the answer's <content>, not the model's thinking.
func TestAnswerText(t *testing.T) {
	assert.Equal(t, "### What is happening\nStill crashing.", answerText("- thinking</thought>\n<content>\n### What is happening\nStill crashing.\n</content>"))
	assert.Equal(t, "plain answer", answerText("  plain answer \n"))
}

func chatFixture(t *testing.T) (*slackFixture, toolcore.NbToolContext) {
	t.Helper()
	f := newSlackFixture(t)
	_, err := f.db.Exec(`CREATE TABLE llm_conversations (id uuid PRIMARY KEY, session_id text, account_id uuid, status text NOT NULL DEFAULT 'WAITING');
		CREATE TABLE llm_conversation_tool_calls (id uuid PRIMARY KEY, conversation_id uuid, message_id uuid, tool_name text, status text, created_at timestamp DEFAULT now());
		CREATE TABLE llm_conversation_messages (id uuid PRIMARY KEY, message_config text);
		ALTER TABLE events ADD COLUMN created_at timestamp DEFAULT now();
		CREATE TABLE event_log_analysis (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), event_id uuid, event_fingerprint text,
			cloud_account_id uuid, analysis_type varchar, recorded_at timestamp DEFAULT now(), updated_at timestamp)`)
	require.NoError(t, err)
	swap(t, &config.Config.RemediationFollowupMaxRuns, 2)
	return f, toolcore.NbToolContext{Ctx: security.NewRequestContextForSuperAdmin(), AccountId: f.account}
}

// The alert's conversation is about the event whose plan the thread and the panel show, in its own account.
func TestAlertOfSession(t *testing.T) {
	f, _ := chatFixture(t)
	newer := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO events VALUES ($1, $2, 'fp-1')`, newer, f.account)
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE events SET created_at = now() - interval '1 hour' WHERE id = $1`, f.event)
	require.NoError(t, err)

	event, fp, err := alertOfSession(f.db, f.account, "event-fp-1")
	require.NoError(t, err)
	assert.Equal(t, newer, event, "the alert's newest event")
	assert.Equal(t, "fp-1", fp)
	_, err = f.db.Exec(`INSERT INTO event_log_analysis (event_id, event_fingerprint, cloud_account_id, analysis_type) VALUES ($1, 'fp-1', $2, 'remediation')`,
		f.event, f.account)
	require.NoError(t, err)
	event, _, err = alertOfSession(f.db, f.account, "event-fp-1")
	require.NoError(t, err)
	assert.Equal(t, f.event, event, "a re-fire without a plan: the chat is about the event whose plan the thread shows")

	_, _, err = alertOfSession(f.db, uuid.NewString(), "event-fp-1")
	assert.Error(t, err, "another account's alert")
	_, _, err = alertOfSession(f.db, f.account, "C1-1700000000.1")
	assert.Error(t, err)

	// An event with no fingerprint is its own alert, in conversation event-<id>; not across accounts.
	bare := uuid.NewString()
	_, err = f.db.Exec(`INSERT INTO events VALUES ($1, $2, '')`, bare, f.account)
	require.NoError(t, err)
	event, _, err = alertOfSession(f.db, f.account, "event-"+bare)
	require.NoError(t, err)
	assert.Equal(t, bare, event)
	_, _, err = alertOfSession(f.db, uuid.NewString(), "event-"+bare)
	assert.Error(t, err)
}

// A proposal from the alert's chat waits for a run and its check to finish, and is capped per 24 hours.
func TestChatProposalRefusal(t *testing.T) {
	f, ctx := chatFixture(t)
	session := "event-fp-1"
	refusal := func() string {
		t.Helper()
		reason, err := chatProposalRefusal(ctx, f.db, session, "fp-1")
		require.NoError(t, err)
		return reason
	}
	require.Empty(t, refusal(), "nothing in flight, under the cap")

	run := f.decision("InProgress", "user-1", `{}`)
	assert.Contains(t, refusal(), "running or its follow-up check has not finished")
	_, err := f.db.Exec(`UPDATE event_resolution SET status = 'Success', data = '{"followup":{"claimed_at":"x"}}' WHERE id = $1`, run)
	require.NoError(t, err)
	assert.Contains(t, refusal(), "follow-up check has not finished")
	_, err = f.db.Exec(`UPDATE event_resolution SET data = '{"followup":{"done_at":"x"}}' WHERE id = $1`, run)
	require.NoError(t, err)
	assert.Empty(t, refusal())

	conversation := uuid.NewString()
	_, err = f.db.Exec(`INSERT INTO llm_conversations VALUES ($1, $2, $3)`, conversation, session, f.account)
	require.NoError(t, err)
	for _, status := range []string{"success", "error", "success"} {
		_, err = f.db.Exec(`INSERT INTO llm_conversation_tool_calls (id, conversation_id, tool_name, status) VALUES ($1, $2, 'propose_fix', $3)`,
			uuid.NewString(), conversation, status)
		require.NoError(t, err)
	}
	assert.Contains(t, refusal(), "2 plans were already proposed", "failed proposals do not count")
	_, err = f.db.Exec(`UPDATE llm_conversation_tool_calls SET created_at = now() - interval '25 hours'`)
	require.NoError(t, err)
	assert.Empty(t, refusal(), "the cap is per 24 hours")
}

// A step that runs after another runs only once that step succeeded on the same plan.
func TestStepOrderRejection(t *testing.T) {
	f := newRunFixture(t)
	stored := storedPlan{Version: f.stored.Version, Plan: RemediationPlan{Actions: []RemediationAction{
		{Action: "Raise limit", Title: "Raise the memory limit", ExecuteCommand: "kubectl set resources deploy/api -n shop --limits=memory=1Gi"},
		{Action: "Restart", ExecuteCommand: runCommand, After: "raise limit"},
	}}}
	ctx := security.NewRequestContextForSuperAdmin()
	decide := func(status, version string) {
		t.Helper()
		_, err := f.db.Exec(`INSERT INTO event_resolution (id, event_id, type, status, data, type_reference_id, resolver_type, resolver_id)
			VALUES ($1, $2, 'CommandExecution', $3, jsonb_build_object('step', '0:apply', 'plan_version', $4::text), 'cmd', 'User', 'user-1')`,
			uuid.NewString(), f.event, status, version)
		require.NoError(t, err)
	}
	assert.Empty(t, stepOrderRejection(ctx, f.event, stored, "0:apply"), "a first step has nothing before it")
	assert.Contains(t, stepOrderRejection(ctx, f.event, stored, "1:apply"), `runs after step 1 (Raise the memory limit)`)
	decide("Success", "another-version")
	assert.NotEmpty(t, stepOrderRejection(ctx, f.event, stored, "1:apply"), "a success on another plan does not count")
	decide("Failed", stored.Version)
	assert.Contains(t, stepOrderRejection(ctx, f.event, stored, "1:apply"), "ask for a new plan if it failed")
	time.Sleep(10 * time.Millisecond)
	decide("Success", stored.Version)
	assert.Empty(t, stepOrderRejection(ctx, f.event, stored, "1:apply"))
}

// A follow-up's plan is built from the panel's last plan inputs for this event, never another firing's or ids.
func TestLastPlanInputs(t *testing.T) {
	f := newRunFixture(t)
	_, err := f.db.Exec(`ALTER TABLE events ADD COLUMN fingerprint text, ADD COLUMN aggregation_key text, ADD COLUMN created_at timestamp DEFAULT now();
		CREATE TABLE event_analysis_mapping (event_id uuid, analysis_id uuid, analysis_type text)`)
	require.NoError(t, err)
	ctx := security.NewRequestContextForSuperAdmin()
	bare := RemediationGenerateRequest{AccountId: f.account, EventId: f.event}
	require.Equal(t, bare, lastPlanInputs(ctx, f.account, f.event), "no stored plan")

	panel := RemediationGenerateRequest{AccountId: "browser-account", EventId: "browser-event", Context: "Threshold suggestion already computed by triage: 80%",
		AvailableArtifacts: []string{"threshold", "alert_rule"}, AlertRule: &RemediationAlertRule{RuleID: "rule-1"}}
	data, err := json.Marshal(struct {
		RemediationPlan
		Generation RemediationTrace `json:"generation"`
	}{runPlan(), RemediationTrace{Request: &panel}})
	require.NoError(t, err)
	_, err = f.db.Exec(`UPDATE event_log_analysis SET analysis = $1, cloud_account_id = $2, event_fingerprint = '', event_aggregation_key = '' WHERE id = $3`,
		string(data), f.account, f.stored.RowId)
	require.NoError(t, err)

	got := lastPlanInputs(ctx, f.account, f.event)
	assert.Equal(t, panel.Context, got.Context)
	assert.Equal(t, panel.AvailableArtifacts, got.AvailableArtifacts)
	assert.Equal(t, "rule-1", got.AlertRule.RuleID)
	assert.Equal(t, f.account, got.AccountId, "the stored ids never replace the caller's")
	assert.Equal(t, f.event, got.EventId)

	other := uuid.NewString()
	_, err = f.db.Exec(`INSERT INTO events (id, cloud_account_id, fingerprint, aggregation_key) VALUES ($1, $2, '', '')`, other, f.account)
	require.NoError(t, err)
	assert.Equal(t, RemediationGenerateRequest{AccountId: f.account, EventId: other}, lastPlanInputs(ctx, f.account, other), "another firing's plan is not this event's")
}

// --- follow-ups ---

// followupFixture's tenant has the loop on; the agent turn, its message and plan generation are stubbed.
type followupFixture struct {
	t       *testing.T
	db      *sqlx.DB
	account string
	event   string

	mu         sync.Mutex
	turns      []string // the session of each turn
	queries    []string // the question of each turn
	turnEnds   []core.ConversationStatus
	busy       int // starts refused because someone else's turn is in the conversation
	during     []func(ctx toolcore.NbToolContext)
	messages   map[string]followupMessage // each turn's message, by id
	proposals  []followupRun
	proposeErr error
}

type followupMessage struct {
	status   core.ConversationStatus
	response string
}

// testPlanJSON is a plan an agent writes for propose_fix.
const testPlanJSON = `{"root_cause":"512Mi limit","summary":"Raise the memory limit","actions":[{"action":"Raise limit","execute_command":"kubectl set resources deploy/api -n shop --limits=memory=1Gi"}]}`

func newFollowupFixture(t *testing.T) *followupFixture {
	t.Helper()
	db := remediationScratchDB(t, "followup", `CREATE TABLE event_resolution (LIKE public.event_resolution INCLUDING ALL);
		CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid NOT NULL, fingerprint text, tenant text NOT NULL DEFAULT 'tenant-1');
		CREATE TABLE cloud_accounts (id uuid PRIMARY KEY, tenant text);
		CREATE TABLE feature_flag (feature_id text, tenant_id text, account_id uuid, status text);
		INSERT INTO feature_flag VALUES ('`+events.RemediationLoopFeature+`', 'tenant-1', NULL, 'enabled')`)
	f := &followupFixture{t: t, db: db, account: uuid.NewString(), event: uuid.NewString(), messages: map[string]followupMessage{}}
	_, err := db.Exec(`INSERT INTO cloud_accounts VALUES ($1, 'tenant-1')`, f.account)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO events VALUES ($1, $2, 'fp-1')`, f.event, f.account)
	require.NoError(t, err)
	withMetastore(t, db)
	swap(t, &followupDelay, 5)
	swap(t, &config.Config.RemediationFollowupMaxRuns, 3)
	swap(t, &goFollowupTurn, func(turn func()) { turn() })
	swap(t, &startFollowupMessage, func(run followupRun, messageId uuid.UUID) (string, bool, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.busy > 0 {
			f.busy--
			return "", false, nil
		}
		f.messages[messageId.String()] = followupMessage{status: core.ConversationStatusInProgress}
		return "conversation-1", true, nil
	})
	swap(t, &runFollowupTurn, func(run followupRun, _ string, messageId uuid.UUID) error {
		f.mu.Lock()
		var do func(toolcore.NbToolContext)
		if len(f.during) > 0 {
			do, f.during = f.during[0], f.during[1:]
		}
		f.mu.Unlock()
		if do != nil {
			do(f.toolCtx(f.account, messageId.String())) // outside the lock: a tool it calls may reach the proposePlan stub
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.turns = append(f.turns, followupSession(run))
		f.queries = append(f.queries, followupQuery(run))
		end := core.ConversationStatusCompleted
		if len(f.turnEnds) > 0 {
			end, f.turnEnds = f.turnEnds[0], f.turnEnds[1:]
		}
		f.messages[messageId.String()] = followupMessage{end, "the deployment is still crash-looping"}
		return nil
	})
	swap(t, &followupTurnState, func(run followupRun) (core.ConversationStatus, string, bool) {
		f.mu.Lock()
		defer f.mu.Unlock()
		m, ok := f.messages[run.MessageId]
		return m.status, m.response, ok
	})
	swap(t, &proposePlan, func(_ *security.RequestContext, accountId, eventId, planJSON string) (RemediationPlan, string, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.proposals = append(f.proposals, followupRun{AccountId: accountId, EventId: eventId, Command: planJSON})
		if f.proposeErr != nil {
			return RemediationPlan{}, "", f.proposeErr
		}
		return RemediationPlan{Summary: "Raise the memory limit", Actions: []RemediationAction{{Action: "Raise limit", ExecuteCommand: "kubectl set resources deploy/api -n shop --limits=memory=1Gi"}}}, "version-2", nil
	})
	return f
}

// toolCtx is a tool call's context in the turn messageId: read-only, as runFollowupTurn runs it.
func (f *followupFixture) toolCtx(accountId, messageId string) toolcore.NbToolContext {
	ctx := core.WithReadOnlyRun(security.NewRequestContextForTenantAccountAdmin("tenant-1", security.GetSystemUserId(), []string{f.account}))
	return toolcore.NbToolContext{Ctx: ctx, AccountId: accountId, MessageId: messageId}
}

// insertRun adds a CommandExecution row for event, created ago before now, and returns its id.
func (f *followupFixture) insertRun(event, status string, ago time.Duration, data map[string]any) string {
	f.t.Helper()
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	require.NoError(f.t, err)
	id := uuid.NewString()
	_, err = f.db.Exec(`INSERT INTO event_resolution (id, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, created_at)
		VALUES ($1, $2, 'CommandExecution', $3, $4, 'kubectl rollout restart deploy/api -n shop', 'User', 'user-1', LOCALTIMESTAMP - make_interval(secs => $5))`,
		id, event, string(raw), status, ago.Seconds())
	require.NoError(f.t, err)
	return id
}

// followup returns the run's data.followup.
func (f *followupFixture) followup(runId string) map[string]any {
	f.t.Helper()
	var raw []byte
	require.NoError(f.t, f.db.Get(&raw, `SELECT COALESCE(data->'followup', 'null'::jsonb) FROM event_resolution WHERE id = $1`, runId))
	var out map[string]any
	require.NoError(f.t, json.Unmarshal(raw, &out))
	return out
}

// ageClaim moves a follow-up's claim three hours into the past.
func (f *followupFixture) ageClaim(runId string) {
	f.t.Helper()
	_, err := f.db.Exec(`UPDATE event_resolution SET data = jsonb_set(data, '{followup,claimed_at}', to_jsonb((now() - interval '3 hours')::text)) WHERE id = $1`, runId)
	require.NoError(f.t, err)
}

// dueRecheck makes the follow-up's scheduled re-check due now.
func (f *followupFixture) dueRecheck(runId string) {
	f.t.Helper()
	_, err := f.db.Exec(`UPDATE event_resolution SET data = jsonb_set(data, '{followup,recheck_at}', to_jsonb((now() - interval '1 second')::text)) WHERE id = $1`, runId)
	require.NoError(f.t, err)
}

// tick runs the job n times: each claims what is due, starts turns and records the turns that ended.
func (f *followupFixture) tick(n int) {
	f.t.Helper()
	for range n {
		require.NoError(f.t, runDueFollowups())
		threadPostsInFlight.Wait() // a tick ends with the thread posts it started
	}
}

func (f *followupFixture) turnCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.turns)
}

// Only settled, due runs in a tenant with the loop on (an account-level flag row does not turn it on) are
// claimed, once across overlapping claimers, into the alert's own conversation.
func TestFollowupClaimsOnlyDueRunsOnce(t *testing.T) {
	f := newFollowupFixture(t)
	due := f.insertRun(f.event, "Success", 6*time.Minute, nil)
	manual := f.insertRun(f.event, "Success", 7*time.Minute, map[string]any{"manual": true})
	for _, notDue := range []struct {
		status string
		ago    time.Duration
		data   map[string]any
	}{
		{"Success", 2 * time.Minute, nil},                             // not due yet
		{"InProgress", 8 * time.Minute, nil},                          // not settled
		{"Declined", 8 * time.Minute, nil},                            // nothing ran
		{"Failed", 8 * time.Minute, map[string]any{"not_sent": true}}, // provably never ran
		{"Success", 2 * time.Hour, nil},                               // more than an hour past due
		{"Success", 8 * time.Minute, map[string]any{"followup": map[string]any{"done_at": "x"}}},
	} {
		f.insertRun(f.event, notDue.status, notDue.ago, notDue.data)
	}
	otherAccount, otherEvent := uuid.NewString(), uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO events VALUES ($1, $2, 'fp-9', 'tenant-2')`, otherEvent, otherAccount)
	require.NoError(t, err)
	f.insertRun(otherEvent, "Success", 8*time.Minute, nil)
	_, err = f.db.Exec(`INSERT INTO feature_flag VALUES ($1, 'tenant-2', $2, 'enabled')`, events.RemediationLoopFeature, otherAccount)
	require.NoError(t, err)

	var wg sync.WaitGroup
	var mu sync.Mutex
	var claimed []string
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runs, err := events.ClaimDueFollowups(t.Context(), f.db, followupDelay, 10)
			assert.NoError(t, err)
			mu.Lock()
			for _, r := range runs {
				claimed = append(claimed, r.Id)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	assert.ElementsMatch(t, []string{due, manual}, claimed)
	assert.Equal(t, "event-fp-1", f.followup(due)["session"])
	again, err := events.ClaimDueFollowups(t.Context(), f.db, followupDelay, 10)
	require.NoError(t, err)
	assert.Empty(t, again, "a claimed run is not claimed again")
}

// One read-only turn in the alert's conversation; its report is recorded from its own message.
func TestFollowupRunsOneTurnAndRecordsIt(t *testing.T) {
	f := newFollowupFixture(t)
	run := f.insertRun(f.event, "Failed", 6*time.Minute, map[string]any{"output": "error: deployments.apps \"api\" not found"})
	f.tick(2)
	assert.Equal(t, []string{"event-fp-1"}, f.turns)
	fu := f.followup(run)
	assert.Equal(t, followupReported, fu["outcome"])
	assert.Equal(t, "the deployment is still crash-looping", fu["summary"])
	assert.NotEmpty(t, fu["message_id"])
	assert.NotEmpty(t, fu["done_at"])
	assert.Contains(t, f.queries[0], "deployments.apps", "the turn reads what the command printed")
	assert.Contains(t, f.queries[0], "get_remediation_plan")
	f.tick(1)
	assert.Equal(t, 1, f.turnCount(), "one turn")
}

// Review of #41596 (5ae0daa2): a check right after a restart reported "healthy" on a pod seconds old, and the
// alert fired again. A change that took effect is reported only after a second look: the first report waits
// for a re-check at the shortest wait, even when the turn asked for none.
func TestFollowupReportsAChangeOnlyAfterASecondLook(t *testing.T) {
	f := newFollowupFixture(t)
	run := f.insertRun(f.event, "Success", 6*time.Minute, map[string]any{"output": "deployment.apps/api restarted"})
	f.tick(2)
	fu := f.followup(run)
	assert.Nil(t, fu["done_at"], "a seconds-old pod is not reported fixed")
	assert.Equal(t, soakReason, fu["recheck_reason"])
	assert.EqualValues(t, 1, fu["rechecks"])
	at, err := time.Parse(time.RFC3339Nano, fu["recheck_at"].(string))
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(time.Duration(recheckMinMinutes)*time.Minute), at, time.Minute)

	f.dueRecheck(run)
	f.tick(2)
	assert.Equal(t, 2, f.turnCount())
	assert.Contains(t, f.queries[1], soakReason)
	fu = f.followup(run)
	assert.Equal(t, followupReported, fu["outcome"], "the second look's report stands")
	assert.NotEmpty(t, fu["done_at"])
}

// How a turn ended is the outcome, and it is never retried.
func TestFollowupOutcomeOfTheTurn(t *testing.T) {
	for name, tc := range map[string]struct {
		end     core.ConversationStatus
		propose bool
		want    string
	}{
		"proposed a plan":     {core.ConversationStatusCompleted, true, followupProposed},
		"stopped by a person": {core.ConversationStatusTerminated, false, followupStopped},
		"turn failed":         {core.ConversationStatusFailed, false, followupNeedsHuman},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFollowupFixture(t)
			run := f.insertRun(f.event, "Success", 6*time.Minute, nil)
			f.turnEnds = []core.ConversationStatus{tc.end}
			if tc.propose {
				f.during = []func(toolcore.NbToolContext){func(ctx toolcore.NbToolContext) {
					_, err := proposeFix(ctx, "the limit is still 512Mi", testPlanJSON)
					assert.NoError(t, err)
				}}
			}
			f.tick(3)
			assert.Equal(t, tc.want, f.followup(run)["outcome"])
			assert.Equal(t, 1, f.turnCount())
		})
	}
}

// Hard limits end a follow-up with no model call: a newer run supersedes it, and past the cap (runs whose
// follow-up ran a turn) a person decides.
func TestFollowupStopsWithoutAModel(t *testing.T) {
	f := newFollowupFixture(t)
	older := f.insertRun(f.event, "Success", 9*time.Minute, nil)
	runs, err := events.ClaimDueFollowups(t.Context(), f.db, followupDelay, 1)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	f.insertRun(f.event, "InProgress", time.Minute, nil)
	f.tick(1)
	assert.Equal(t, followupSuperseded, f.followup(older)["outcome"])

	// The same alert on another row (same fingerprint) counts towards the cap; a declined step does not.
	f2 := newFollowupFixture(t)
	refire := uuid.NewString()
	_, err = f2.db.Exec(`INSERT INTO events VALUES ($1, $2, 'fp-1')`, refire, f2.account)
	require.NoError(t, err)
	checked := map[string]any{"followup": map[string]any{"done_at": "x", "message_id": uuid.NewString()}}
	f2.insertRun(refire, "Success", 3*time.Hour, checked)
	f2.insertRun(f2.event, "Failed", 2*time.Hour, checked)
	f2.insertRun(f2.event, "Success", 100*time.Minute, checked)
	f2.insertRun(f2.event, "Declined", 90*time.Minute, nil)
	// Live run, 21 Oct: a long plan's earlier steps are superseded with no turn; they do not use up the cap,
	// or the last step of a 4-step plan got no check at all.
	f2.insertRun(f2.event, "Success", 20*time.Minute, map[string]any{"followup": map[string]any{"done_at": "x", "outcome": followupSuperseded}})
	third := f2.insertRun(f2.event, "Success", 6*time.Minute, nil)
	f2.tick(1)
	fu := f2.followup(third)
	assert.Equal(t, followupNeedsHuman, fu["outcome"])
	assert.Equal(t, "attempt_limit", fu["reason"])
	assert.Zero(t, f.turnCount()+f2.turnCount(), "no model call")
}

// Live run, 21 Oct: a 4-step plan run step by step leaves three superseded follow-ups with no turn; the last
// step's follow-up still checks the fix, under a cap of 3. Review of #41596 (5cbebfa6): the cap counts plans, so
// a plan whose steps were each checked still gets its last step checked.
func TestFollowupChecksTheLastStepOfALongPlan(t *testing.T) {
	f := newFollowupFixture(t)
	for _, ago := range []time.Duration{40 * time.Minute, 30 * time.Minute, 20 * time.Minute} {
		f.insertRun(f.event, "Success", ago, map[string]any{"followup": map[string]any{"done_at": "x", "outcome": followupSuperseded}})
	}
	last := f.insertRun(f.event, "Success", 6*time.Minute, nil)
	f.tick(1)
	assert.NotEqual(t, followupNeedsHuman, f.followup(last)["outcome"])
	assert.Equal(t, 1, f.turnCount(), "the last step is checked")

	swap(t, &config.Config.RemediationFollowupMaxRuns, 3)
	checked := func(i int, plan string) map[string]any {
		return map[string]any{"plan_version": plan, "followup": map[string]any{"done_at": "x", "outcome": followupReported, "message_id": fmt.Sprintf("m%d", i)}}
	}
	plan := newFollowupFixture(t)
	for i, ago := range []time.Duration{40 * time.Minute, 30 * time.Minute, 20 * time.Minute} {
		plan.insertRun(plan.event, "Success", ago, checked(i, "v1"))
	}
	step4 := plan.insertRun(plan.event, "Success", 6*time.Minute, map[string]any{"plan_version": "v1"})
	plan.tick(1)
	assert.NotEqual(t, followupNeedsHuman, plan.followup(step4)["outcome"], "three checked steps of the same plan are one plan")
	assert.Equal(t, 1, plan.turnCount(), "the finishing step is checked")

	// Three other plans checked: the fourth plan is past the cap.
	plans := newFollowupFixture(t)
	for i, ago := range []time.Duration{40 * time.Minute, 30 * time.Minute, 20 * time.Minute} {
		plans.insertRun(plans.event, "Success", ago, checked(i, fmt.Sprintf("v%d", i)))
	}
	fourth := plans.insertRun(plans.event, "Success", 6*time.Minute, map[string]any{"plan_version": "v9"})
	plans.tick(1)
	assert.Equal(t, "attempt_limit", plans.followup(fourth)["reason"])

	// A run whose turn could not start yet (a busy conversation leaves message_id "") is not a check.
	f2 := newFollowupFixture(t)
	for i, ago := range []time.Duration{30 * time.Minute, 20 * time.Minute} {
		f2.insertRun(f2.event, "Success", ago, checked(i, fmt.Sprintf("v%d", i)))
	}
	waited := f2.insertRun(f2.event, "Failed", 6*time.Minute, nil)
	f2.busy = 1
	f2.tick(3)
	assert.Equal(t, followupReported, f2.followup(waited)["outcome"], "two plans were checked before it: it is the third, not past the cap")
}

// A follow-up never pre-empts a person's turn: it starts on a later tick, and gives up if the conversation stays busy.
func TestFollowupWaitsForABusyConversation(t *testing.T) {
	f := newFollowupFixture(t)
	run := f.insertRun(f.event, "Failed", 6*time.Minute, nil)
	f.busy = 2
	f.tick(2)
	assert.Zero(t, f.turnCount())
	assert.Empty(t, f.followup(run)["message_id"], "a turn that did not start is not recorded")
	f.tick(2)
	assert.Equal(t, 1, f.turnCount())
	assert.Equal(t, followupReported, f.followup(run)["outcome"])

	f2 := newFollowupFixture(t)
	busy := f2.insertRun(f2.event, "Success", 6*time.Minute, nil)
	f2.busy = 100
	f2.tick(2)
	f2.ageClaim(busy)
	f2.tick(2)
	fu := f2.followup(busy)
	assert.Equal(t, followupNeedsHuman, fu["outcome"])
	assert.Equal(t, "conversation_busy", fu["reason"])
}

// Claims never wait on turns: hung turns do not hold a new run back, and one hung for hours is given up.
func TestFollowupHungTurnsDoNotBlockClaims(t *testing.T) {
	f := newFollowupFixture(t)
	var hung []string
	for i := range 4 {
		event := uuid.NewString()
		_, err := f.db.Exec(`INSERT INTO events VALUES ($1, $2, $3)`, event, f.account, "fp-hung-"+event)
		require.NoError(t, err)
		hung = append(hung, f.insertRun(event, "Success", time.Duration(10+i)*time.Minute, nil))
		f.turnEnds = append(f.turnEnds, core.ConversationStatusInProgress)
	}
	f.tick(3)
	require.Equal(t, 4, f.turnCount(), "every hung follow-up started its turn")
	fresh := f.insertRun(f.event, "Failed", 6*time.Minute, nil)
	f.tick(2)
	assert.Equal(t, followupReported, f.followup(fresh)["outcome"], "a new run is followed up while the others hang")

	f.ageClaim(hung[0])
	f.tick(1)
	assert.Equal(t, "turn_stuck", f.followup(hung[0])["reason"])
	assert.Nil(t, f.followup(hung[1])["done_at"], "the others are still waited for")
}

// propose_fix finds the run by the turn's own message and takes the event and account from the run row; it
// proposes once and refuses another account, a turn that is no follow-up's, a newer run and an ended follow-up.
func TestProposeFix(t *testing.T) {
	f := newFollowupFixture(t)
	run := f.insertRun(f.event, "Success", 6*time.Minute, nil)
	_, err := events.ClaimDueFollowups(t.Context(), f.db, followupDelay, 1)
	require.NoError(t, err)
	turn := uuid.NewString()
	set, err := events.SetFollowupTurn(t.Context(), f.db, run, "", turn)
	require.NoError(t, err)
	require.True(t, set)
	call := func(messageId, account string) (string, error) {
		return proposeFix(f.toolCtx(account, messageId), "the pod is OOMKilled again at 512Mi; the restart did not change the limit", testPlanJSON)
	}

	_, err = call(turn, uuid.NewString())
	require.ErrorContains(t, err, "not one", "a run of another account")
	_, err = call(uuid.NewString(), f.account)
	require.ErrorContains(t, err, "not one", "a read-only turn that is no follow-up's")
	require.Empty(t, f.proposals)

	f.proposeErr = errors.New("model unavailable")
	_, err = call(turn, f.account)
	require.ErrorContains(t, err, "was not stored")
	f.proposeErr = nil
	out, err := call(turn, f.account)
	require.NoError(t, err, "a failed proposal frees the slot")
	assert.Contains(t, out, "A person must confirm")
	require.Len(t, f.proposals, 2)
	assert.Equal(t, followupRun{AccountId: f.account, EventId: f.event, Command: testPlanJSON}, f.proposals[1], "the run's event and account, the model's plan")
	fu := f.followup(run)
	assert.Equal(t, "version-2", fu["proposed_plan_version"])
	assert.Contains(t, fu["findings"], "OOMKilled again")
	_, err = call(turn, f.account)
	require.ErrorContains(t, err, "proposes once")

	f2 := newFollowupFixture(t)
	run2 := f2.insertRun(f2.event, "Success", 6*time.Minute, nil)
	_, err = events.ClaimDueFollowups(t.Context(), f2.db, followupDelay, 1)
	require.NoError(t, err)
	_, err = events.SetFollowupTurn(t.Context(), f2.db, run2, "", "turn-2")
	require.NoError(t, err)
	f2.insertRun(f2.event, "InProgress", time.Minute, nil)
	ctx := f2.toolCtx(f2.account, "turn-2")
	_, err = proposeFix(ctx, "findings", testPlanJSON)
	require.ErrorContains(t, err, "newer remediation run")
	_, err = f2.db.Exec(`UPDATE event_resolution SET data = jsonb_set(data, '{followup,done_at}', '"x"') WHERE id = $1`, run2)
	require.NoError(t, err)
	_, err = proposeFix(ctx, "findings", testPlanJSON)
	require.ErrorContains(t, err, "not one")
	assert.Empty(t, f2.proposals)
}

// The last check the cap allows may still propose: its own run counts the same inside its turn as before it.
func TestTheLastAllowedCheckMayPropose(t *testing.T) {
	swap(t, &config.Config.RemediationFollowupMaxRuns, 3)
	f := newFollowupFixture(t)
	for i, ago := range []time.Duration{30 * time.Minute, 20 * time.Minute} {
		f.insertRun(f.event, "Success", ago, map[string]any{"followup": map[string]any{"done_at": "x", "outcome": followupReported, "message_id": fmt.Sprintf("m%d", i)}})
	}
	run := f.insertRun(f.event, "Success", 6*time.Minute, nil)
	_, err := events.ClaimDueFollowups(t.Context(), f.db, followupDelay, 1)
	require.NoError(t, err)
	_, err = events.SetFollowupTurn(t.Context(), f.db, run, "", "turn-3")
	require.NoError(t, err)
	_, err = proposeFix(f.toolCtx(f.account, "turn-3"), "the restart did not hold", testPlanJSON)
	require.NoError(t, err, "two checks ran before this one: it is the third, within the cap")
}

// A plan an agent wrote is refused when validation removed any of its actions, with what was removed, so the
// agent corrects it: a live proposal lost its flag step (a command over the length limit) and was stored with
// the restart alone, while the agent told the person the flag was handled.
func TestWrittenPlanRefusal(t *testing.T) {
	long := "kubectl patch configmap flagd-config -n demo --type merge -p '" + strings.Repeat("x", maxRemediationCommandLen) + "'"
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "patch_flag", ExecuteCommand: long},
		{Action: "restart", ExecuteCommand: "kubectl rollout restart deployment/recommendation -n demo"},
	}}
	trace := RemediationTrace{Validation: validatePlanCommands(&plan)}
	require.Len(t, plan.Actions, 1, "the over-long command is dropped")
	err := writtenPlanRefusal(plan, trace, 2)
	require.ErrorContains(t, err, "1 of 2 actions survived")
	assert.ErrorContains(t, err, "patch_flag")
	assert.NoError(t, writtenPlanRefusal(plan, RemediationTrace{}, 1))
	assert.Error(t, writtenPlanRefusal(RemediationPlan{}, RemediationTrace{}, 0), "nothing to run is refused too")
}

// An event with an empty fingerprint is its own alert: it is superseded and has its own event-<id> conversation.
func TestFollowupWithEmptyFingerprint(t *testing.T) {
	f := newFollowupFixture(t)
	event := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO events VALUES ($1, $2, '')`, event, f.account)
	require.NoError(t, err)
	older := f.insertRun(event, "Success", 9*time.Minute, nil)
	_, err = events.ClaimDueFollowups(t.Context(), f.db, followupDelay, 1)
	require.NoError(t, err)
	assert.Equal(t, "event-"+event, f.followup(older)["session"])
	f.insertRun(event, "Success", time.Minute, nil)
	f.tick(1)
	assert.Equal(t, followupSuperseded, f.followup(older)["outcome"])
}

// schedule_recheck makes the follow-up wait and run another turn, told why; it is bounded (minutes, count,
// not after a proposal) and dropped when a person stops the turn or the turn fails.
func TestFollowupRecheck(t *testing.T) {
	f := newFollowupFixture(t)
	run := f.insertRun(f.event, "Success", 6*time.Minute, nil)
	var duringErr error
	f.during = []func(toolcore.NbToolContext){func(ctx toolcore.NbToolContext) {
		for _, m := range []int{4, 31} {
			if _, err := scheduleRecheck(ctx, m, "p95"); err == nil || !strings.Contains(err.Error(), "from 5 to 30") {
				duringErr = errors.Join(duringErr, errors.New("minutes out of range accepted"))
			}
		}
		_, err := scheduleRecheck(ctx, 10, "p95 is averaged over 10 minutes that still include the restart")
		duringErr = errors.Join(duringErr, err)
	}}
	f.tick(2)
	require.NoError(t, duringErr)

	fu := f.followup(run)
	assert.Nil(t, fu["done_at"], "it waits instead of ending")
	assert.Nil(t, fu["recheck_request"])
	assert.EqualValues(t, 1, fu["rechecks"])
	assert.Equal(t, "the deployment is still crash-looping", fu["summary"], "the report so far is shown while it waits")
	at, err := time.Parse(time.RFC3339Nano, fu["recheck_at"].(string))
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(10*time.Minute), at, time.Minute)
	f.tick(2)
	assert.Equal(t, 1, f.turnCount(), "not due before its time")

	_, err = f.db.Exec(`UPDATE event_resolution SET data = jsonb_set(data, '{followup,recheck_at}', to_jsonb((now() - interval '1 second')::text)) WHERE id = $1`, run)
	require.NoError(t, err)
	f.tick(2)
	assert.Equal(t, []string{"event-fp-1", "event-fp-1"}, f.turns)
	assert.Contains(t, f.queries[1], "This is re-check 1, which you asked for: p95 is averaged")
	fu = f.followup(run)
	assert.Equal(t, followupReported, fu["outcome"])
	assert.NotEmpty(t, fu["done_at"])
	assert.Nil(t, fu["recheck_at"])

	// Past followupMaxRechecks the tool refuses.
	f2 := newFollowupFixture(t)
	run2 := f2.insertRun(f2.event, "Success", 6*time.Minute, map[string]any{"followup": map[string]any{"claimed_at": time.Now().Format(time.RFC3339Nano), "rechecks": 2, "message_id": "turn-2"}})
	_, err = scheduleRecheck(f2.toolCtx(f2.account, "turn-2"), 10, "again")
	require.ErrorContains(t, err, "re-checks")
	assert.Nil(t, f2.followup(run2)["recheck_request"])

	// A proposal ends the follow-up: a re-check is refused after it, and one requested before it is dropped.
	f3 := newFollowupFixture(t)
	run3 := f3.insertRun(f3.event, "Failed", 6*time.Minute, nil)
	f3.during = []func(toolcore.NbToolContext){func(ctx toolcore.NbToolContext) {
		_, duringErr = scheduleRecheck(ctx, 10, "wait for the rollout")
		if duringErr == nil {
			_, duringErr = proposeFix(ctx, "the limit is still 512Mi", testPlanJSON)
		}
		if duringErr == nil {
			if _, err := scheduleRecheck(ctx, 10, "after proposing"); err == nil {
				duringErr = errors.New("re-check after a proposal accepted")
			}
		}
	}}
	f3.tick(2)
	require.NoError(t, duringErr)
	fu = f3.followup(run3)
	assert.Equal(t, followupProposed, fu["outcome"])
	assert.NotEmpty(t, fu["done_at"])
	assert.Nil(t, fu["recheck_at"])
	assert.Equal(t, 1, f3.turnCount())

	for _, end := range []core.ConversationStatus{core.ConversationStatusTerminated, core.ConversationStatusFailed} {
		f4 := newFollowupFixture(t)
		run4 := f4.insertRun(f4.event, "Success", 6*time.Minute, nil)
		f4.during = []func(toolcore.NbToolContext){func(ctx toolcore.NbToolContext) { _, duringErr = scheduleRecheck(ctx, 10, "wait for the rollout") }}
		f4.turnEnds = []core.ConversationStatus{end}
		f4.tick(3)
		require.NoError(t, duringErr)
		fu = f4.followup(run4)
		assert.NotEmpty(t, fu["done_at"], end)
		assert.Nil(t, fu["recheck_at"], end)
		assert.Equal(t, 1, f4.turnCount(), end)
	}
}

// --- Slack ---

type slackFixture struct {
	t       *testing.T
	db      *sqlx.DB
	account string
	event   string
	tenant  string
}

func newSlackFixture(t *testing.T) *slackFixture {
	t.Helper()
	db := remediationScratchDB(t, "slack", `CREATE TABLE event_resolution (LIKE public.event_resolution INCLUDING ALL);
		CREATE TABLE events (id uuid PRIMARY KEY, cloud_account_id uuid NOT NULL, fingerprint text);
		CREATE TABLE cloud_accounts (id uuid PRIMARY KEY, tenant uuid)`)
	f := &slackFixture{t: t, db: db, account: uuid.NewString(), event: uuid.NewString(), tenant: uuid.NewString()}
	_, err := db.Exec(`INSERT INTO cloud_accounts VALUES ($1, $2)`, f.account, f.tenant)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO events VALUES ($1, $2, 'fp-1')`, f.event, f.account)
	require.NoError(t, err)
	return f
}

func (f *slackFixture) decision(status, resolver, data string) string {
	f.t.Helper()
	id := uuid.NewString()
	_, err := f.db.Exec(`INSERT INTO event_resolution (id, event_id, type, data, status, type_reference_id, resolver_type, resolver_id, status_message)
		VALUES ($1, $2, 'CommandExecution', $3, $4, 'kubectl', 'User', $5, 'msg')`, id, f.event, data, status, resolver)
	require.NoError(f.t, err)
	return id
}

func stubSlackPlan(t *testing.T, version string, ok bool) {
	t.Helper()
	swap(t, &loadSlackPlan, func(*security.RequestContext, string, string) (RemediationPlan, string, bool) {
		if !ok {
			return RemediationPlan{}, "", false
		}
		return RemediationPlan{RootCause: "bad tag", Summary: "roll back", Actions: []RemediationAction{
			{Action: "Roll back", Kind: "mitigation", Confidence: 92, ExecuteCommand: "kubectl rollout undo deployment/web -n shop", Delivery: DeliveryRun},
			{Action: "Fix the chart", Kind: "fix", Delivery: DeliverySource, DeliveryReason: "change it in the repository"},
		}}, version, true
	})
}

type capturedSends struct {
	mu     sync.Mutex
	calls  []threadPosts
	status []int // the status of each send in turn; 0 is a transport error; 200 once they run out
	done   chan struct{}
}

func captureSlackSends(t *testing.T, statuses ...int) *capturedSends {
	t.Helper()
	c := &capturedSends{status: statuses, done: make(chan struct{}, 10)}
	swap(t, &slackSendBackoff, time.Millisecond)
	swap(t, &postToAlertThread, func(_ context.Context, posts threadPosts) (int, error) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.calls = append(c.calls, posts)
		status := 200
		if len(c.status) > 0 {
			status, c.status = c.status[0], c.status[1:]
		}
		c.done <- struct{}{}
		if status == 0 {
			return 0, errors.New("connection refused")
		}
		return status, nil
	})
	return c
}

func (c *capturedSends) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *capturedSends) wait(t *testing.T) {
	t.Helper()
	select {
	case <-c.done:
	case <-time.After(5 * time.Second):
		t.Fatal("no send")
	}
}

func slackOn(t *testing.T, account string, on bool) {
	t.Helper()
	swap(t, &events.RemediationLoopOn, func(accountId string) bool { return on && accountId == account })
	swap(t, &config.Config.NotificationServerToken, "token")
}

// The cards are the panel's state: the current plan with each step's decision on that plan, and each run's follow-up.
func TestBuildRemediationSlackCards(t *testing.T) {
	f := newSlackFixture(t)
	stubSlackPlan(t, "pv-2", true)
	f.decision("Declined", "user-old", `{"step":"0:apply","plan_version":"pv-1"}`)
	run := f.decision("Success", "user-1", `{"step":"0:apply","plan_version":"pv-2","source":"slack","exit_code_reported":true,"command":"kubectl rollout undo deployment/web -n shop",
		"followup":{"summary":"pods are ready","recheck_at":"2026-10-16T10:16:22Z","recheck_reason":"readiness delay"}}`)

	cards, err := buildRemediationSlackCards(security.NewRequestContextForSuperAdmin(), f.db, f.account, f.event)
	require.NoError(t, err)
	assert.Equal(t, f.tenant, cards.TenantId)
	assert.Equal(t, "fp-1", cards.Fingerprint)
	require.Len(t, cards.Cards, 2)

	plan := cards.Cards[0]
	assert.Equal(t, "pv-2", plan.PlanVersion)
	require.Len(t, plan.Steps, 2)
	assert.Equal(t, "Success", plan.Steps[0].State, "the decision on an older plan does not mark the step")
	assert.True(t, plan.Steps[0].ExitCodeReported)
	assert.True(t, plan.Steps[0].Runnable)
	assert.Empty(t, plan.Steps[1].State)
	assert.False(t, plan.Steps[1].Runnable, "a source change is not run from the card")

	followup := cards.Cards[1]
	assert.Equal(t, "followup", followup.Kind)
	assert.Equal(t, "2026-10-16T10:16:22Z", followup.RecheckAt)
	assert.Empty(t, followup.Outcome, "it is still waiting")

	_, err = f.db.Exec(`UPDATE event_resolution SET data = jsonb_set(data, '{followup}', data->'followup' || '{"done_at":"x","outcome":"reported"}') WHERE id = $1`, run)
	require.NoError(t, err)
	later, err := buildRemediationSlackCards(security.NewRequestContextForSuperAdmin(), f.db, f.account, f.event)
	require.NoError(t, err)
	assert.Equal(t, "reported", later.Cards[1].Outcome)
	assert.Empty(t, later.Cards[1].RecheckAt)
}

// Without a plan there are no cards, and another account's event is not read.
func TestBuildRemediationSlackCardsWithoutPlan(t *testing.T) {
	f := newSlackFixture(t)
	stubSlackPlan(t, "", false)
	cards, err := buildRemediationSlackCards(security.NewRequestContextForSuperAdmin(), f.db, f.account, f.event)
	require.NoError(t, err)
	assert.Empty(t, cards.Cards)
	_, err = buildRemediationSlackCards(security.NewRequestContextForSuperAdmin(), f.db, uuid.NewString(), f.event)
	assert.Error(t, err)
}

// Nothing is posted unless the loop is on for the event's account and the notification token is set.
func TestPostPlanToThreadIsOffUnlessTheLoopIsOn(t *testing.T) {
	c := captureSlackSends(t)
	for name, tc := range map[string]struct {
		loopAccount string
		on          bool
		event       string
	}{
		"loop off":       {"acct-1", false, "event-1"},
		"another tenant": {"acct-2", true, "event-1"},
		"no event":       {"acct-1", true, ""},
		"empty account":  {"", true, "event-1"},
	} {
		t.Run(name, func(t *testing.T) {
			slackOn(t, tc.loopAccount, tc.on)
			postPlanToThread(security.NewRequestContextForSuperAdmin(), "acct-1", tc.event)
			time.Sleep(20 * time.Millisecond)
			assert.Zero(t, c.count())
		})
	}
	slackOn(t, "acct-1", true)
	swap(t, &config.Config.NotificationServerToken, "")
	assert.True(t, remediationSlackOn("acct-1"), "no token: sent without X-ACTION-TOKEN, like every other notifications-server call")
}

// A send is retried on a failure or a server error, not on a refusal; 202 (thread pending) is final.
func TestSendToAlertThreadRetries(t *testing.T) {
	for name, tc := range map[string]struct {
		statuses []int
		want     int
	}{
		"server error then ok": {[]int{503, 200}, 2},
		"unreachable 3 times":  {[]int{0, 0, 0}, 3},
		"refused":              {[]int{400}, 1},
		"no thread yet is ok":  {[]int{202}, 1},
	} {
		t.Run(name, func(t *testing.T) {
			c := captureSlackSends(t, tc.statuses...)
			sendToAlertThread(context.Background(), slog.Default(), threadPosts{EventId: "e1"})
			assert.Equal(t, tc.want, c.count())
		})
	}
}

// A plan goes to the alert's thread once per version; a step NudgeBee runs carries the token naming its plan
// version and step, and one it does not says why.
func TestPostPlanToThread(t *testing.T) {
	f := newSlackFixture(t)
	stubSlackPlan(t, "pv-1", true)
	withMetastore(t, f.db)
	slackOn(t, f.account, true)
	c := captureSlackSends(t)
	postPlanToThread(security.NewRequestContextForSuperAdmin(), f.account, f.event)
	c.wait(t)
	require.Equal(t, 1, c.count())
	sent := c.calls[0]
	assert.Equal(t, "plan:"+f.event+":pv-1", sent.Key)
	require.NotEmpty(t, sent.TenantId)
	assert.Contains(t, sent.Posts[0].Text, "### Proposed fix")
	assert.Empty(t, sent.Posts[0].Token)
	var tokens int
	for i, post := range sent.Posts[1:] {
		if post.Token != "" {
			tokens++
			assert.Equal(t, fmt.Sprintf("remediation:%s:%s:%s:pv-1:%d", sent.TenantId, f.account, f.event, i), post.Token)
		} else {
			assert.Contains(t, post.Text, "**Manual step:**")
		}
	}
	assert.Positive(t, tokens)
}

// A decision on the panel or in chat is noted in the thread, after the plan (posted once per version, so sent
// again here in case a notifications-server outage lost it); a Slack click shows its own outcome.
func TestNoteStepInThread(t *testing.T) {
	f := newSlackFixture(t)
	stubSlackPlan(t, "pv-1", true)
	withMetastore(t, f.db)
	slackOn(t, f.account, true)
	c := captureSlackSends(t)
	stored := storedPlan{Plan: RemediationPlan{Actions: []RemediationAction{{Action: "Raise limit"}, {Action: "Restart"}}}}
	for _, source := range []string{slackSource, chatSource} {
		noteStepInThread(security.NewRequestContextForSuperAdmin(), RemediationExecuteRequest{AccountId: f.account, EventId: f.event, Source: source}, stored, "1:apply", "ran, success")
	}
	c.wait(t)
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, 2, c.count(), "the Slack click is not noted")
	assert.Equal(t, "plan:"+f.event+":pv-1", c.calls[0].Key)
	assert.Equal(t, "**Step 2 · Restart:** ran, success (from the alert's chat).", c.calls[1].Posts[0].Text)
}

// A settled follow-up check goes to the thread as the agent's report, or as why there was none; a superseded
// one does not, the newer run's check does.
func TestFollowupPostText(t *testing.T) {
	swap(t, &config.Config.RemediationFollowupMaxRuns, 3)
	run := followupRun{Data: []byte(`{"step":"1:apply"}`)}
	report := "**Status:** fixed.\n**Next:** nothing to run."
	assert.Equal(t, "### Follow-up check, after step 2\n"+report, followupPostText(run, map[string]any{"outcome": followupReported, "summary": report}, ""))
	assert.Contains(t, followupPostText(run, map[string]any{"outcome": followupReported, "summary": report}, "2026-10-21T10:16:22.5+00:00"),
		"**Re-check:** NudgeBee looks again at 10:16 UTC.")
	assert.Contains(t, followupPostText(followupRun{Data: []byte(`{"step":"0:rollback"}`)}, map[string]any{"outcome": followupReported, "summary": report}, ""),
		"after undoing step 1")
	capped := followupPostText(run, map[string]any{"outcome": followupNeedsHuman, "reason": "attempt_limit"}, "")
	assert.Contains(t, capped, "not checked: 3 plans of this alert were already checked in 24 hours")
	assert.Contains(t, capped, "a person decides")
	assert.Contains(t, followupPostText(run, map[string]any{"outcome": followupFailed, "reason": "database"}, ""), "**Status:** not checked.")
	assert.Empty(t, followupPostText(run, map[string]any{"outcome": followupSuperseded}, ""))
}

// A note never claims a success that was not confirmed, nor a failure for a command never sent.
func TestRunNoteWording(t *testing.T) {
	assert.Equal(t, "success", runOutcomeText(events.RunStatusSuccess, runFacts{ExitCodeReported: true}))
	assert.Contains(t, runOutcomeText(events.RunStatusSuccess, runFacts{}), "unverified")
	assert.Equal(t, "failed, not run", runOutcomeText(events.RunStatusFailed, runFacts{NotSent: true}))
	// The thread's note: an undo is not called a run, and a command that never went out is not said to have run.
	assert.Equal(t, "rolled back, success", runNoteText(RemediationSlotRollback, events.RunStatusSuccess, runFacts{ExitCodeReported: true}))
	assert.Equal(t, "ran, failed", runNoteText(RemediationSlotExecute, events.RunStatusFailed, runFacts{}))
	assert.NotContains(t, runNoteText(RemediationSlotExecute, events.RunStatusFailed, runFacts{NotSent: true}), "ran")
}

// After an investigation the alert's plan is asked of Nubi's background planner, never the one-shot planner,
// and only with the loop on.
func TestPlanAfterInvestigationAsksThePlanner(t *testing.T) {
	var asked []planRequestInput
	swap(t, &requestPlanAfterInvestigation, func(_ *security.RequestContext, _, eventId string, in planRequestInput) (events.PlanningRequest, string, error) {
		asked = append(asked, in)
		return events.PlanningRequest{EventId: eventId}, "", nil
	})
	planAfterInvestigation(security.NewRequestContextForSuperAdmin(), "acct", "evt")
	planAfterInvestigation(security.NewRequestContextForSuperAdmin(), "acct", " ")
	require.Len(t, asked, 1, "one request per completed investigation; none without an event")
	assert.Equal(t, planTurnAuto, asked[0].Kind)
	assert.Empty(t, asked[0].By, "nobody asked: NudgeBee did")
}

// One plan build per alert at a time: a second caller waits and is told it waited; a caller that gives up is told so.
func TestPlanBuildGuard(t *testing.T) {
	g := &planBuildGuard{busy: map[string]chan struct{}{}}
	waited, err := g.claim(context.Background(), "e1")
	require.NoError(t, err)
	assert.False(t, waited, "nothing in flight")
	claimed := make(chan bool)
	go func() {
		waited, _ := g.claim(context.Background(), "e1")
		claimed <- waited
	}()
	select {
	case <-claimed:
		t.Fatal("claimed while another holds the slot")
	case <-time.After(50 * time.Millisecond):
	}
	g.release("e1")
	assert.True(t, <-claimed, "claimed once the first ended, after waiting")

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = g.claim(cancelled, "e1")
	assert.Error(t, err, "a caller that gives up is told so")
	g.release("e1")
}

// The send is JSON with its content type and the service token: notifications-server refuses anything else.
func TestPostToAlertThreadSendsJSONWithTheToken(t *testing.T) {
	var got struct {
		contentType, token, path string
		body                     threadPosts
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.contentType, got.token, got.path = r.Header.Get("Content-Type"), r.Header.Get("X-ACTION-TOKEN"), r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		w.WriteHeader(200)
	}))
	defer server.Close()
	swap(t, &config.Config.NotificationServerUrl, server.URL)
	swap(t, &config.Config.NotificationServerToken, "secret")

	status, err := postToAlertThread(context.Background(), threadPosts{EventId: "e1", Key: "plan:e1:v", Posts: []threadPost{{Text: "x", Token: "remediation:t:a:e1:v:0"}}})
	require.NoError(t, err)
	assert.Equal(t, 200, status)
	assert.Equal(t, "/api/remediation/thread", got.path)
	assert.Equal(t, "application/json", got.contentType)
	assert.Equal(t, "secret", got.token)
	assert.Equal(t, "remediation:t:a:e1:v:0", got.body.Posts[0].Token)

	// No token configured: still sent, without the header, like every other notifications-server call.
	swap(t, &config.Config.NotificationServerToken, "")
	_, err = postToAlertThread(context.Background(), threadPosts{EventId: "e1"})
	require.NoError(t, err)
	assert.Equal(t, "application/json", got.contentType)
	assert.Empty(t, got.token)
}

// A step's After links to the step it names, and to nothing when it names no step of the plan.
func TestLinkAfterSteps(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "Raise the memory limit"},
		{Action: "Restart web", After: "raise the memory limit"},
		{Action: "Scale up", After: "a step that is not in the plan"},
	}}
	steps := []slackCardStep{{}, {}, {}}
	linkAfterSteps(plan, steps)
	assert.Equal(t, []int{0, 1, 0}, []int{steps[0].AfterStep, steps[1].AfterStep, steps[2].AfterStep})
}

// #40734 live run: the executor hands a tool its input's "command" as input.Command and the other fields as
// Arguments, so run_step read an empty command and refused every apply as "not step N's command".
func TestRunStepInput(t *testing.T) {
	const command = "kubectl rollout restart deployment web -n demo"
	assert.Equal(t, runStepRequest{PlanVersion: "v1", Step: 3, Action: "apply", Command: command},
		runStepInput(toolcore.NBToolCallRequest{Command: command, Arguments: map[string]any{"plan_version": "v1", "step": float64(3), "action": "Apply", "checked": false}}))
	assert.Equal(t, command, runStepInput(toolcore.NBToolCallRequest{Arguments: map[string]any{"command": command, "step": 1, "action": "apply"}}).Command,
		"a command in Arguments (a direct call) still counts")
}

// Live run, 20 Oct: the confirmation key was the tool's name, so approving step 2 in the alert's chat also
// ran step 3 without asking. Each step's action is now its own approval.
func TestRunStepConfirmsEachStep(t *testing.T) {
	var scope toolcore.ToolConfirmationScope = RunStepTool{}
	step2 := scope.ConfirmationKey(`{"plan_version":"v1","step":2,"action":"apply","command":"kubectl patch cm x"}`)
	assert.NotEqual(t, step2, scope.ConfirmationKey(`{"plan_version":"v1","step":3,"action":"apply","command":"kubectl rollout restart deploy/x"}`))
	assert.NotEqual(t, step2, scope.ConfirmationKey(`{"plan_version":"v1","step":2,"action":"rollback","command":"kubectl patch cm y"}`))
	assert.NotEqual(t, step2, scope.ConfirmationKey(`{"plan_version":"v2","step":2,"action":"apply","command":"kubectl patch cm x"}`))
	assert.Equal(t, step2, scope.ConfirmationKey(`{"plan_version":"v1","step":2,"action":"Apply","command":"kubectl patch cm x "}`), "the same step's action is one approval")
}

// The alert's tools classify their own calls: the follow-up check's are reads (no person approves them), and
// run_step is a write, so a person confirms every call.
func TestAlertToolsClassifyThemselves(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	for name, want := range map[string]toolcore.ToolRequestType{toolcore.ToolGetRemediationPlan: toolcore.ToolRequestTypeRead,
		toolcore.ToolProposeFix: toolcore.ToolRequestTypeRead, toolcore.ToolScheduleRecheck: toolcore.ToolRequestTypeRead, toolcore.ToolRunStep: toolcore.ToolRequestTypeCreate} {
		tool, ok := toolcore.GetNBTool("account-1", name)
		require.True(t, ok, name)
		inference, ok := tool.(toolcore.ToolRequestInference)
		require.True(t, ok, name)
		got, err := inference.InferToolRequestType(ctx, name, "{}")
		require.NoError(t, err)
		assert.Equal(t, want, got, name)
	}
}

type sessionDao struct {
	core.IConversationDao
	session string
}

func (d sessionDao) GetConversation(string) (core.Conversation, error) {
	return core.Conversation{SessionID: d.session}, nil
}

// The plan tools are listed for every conversation of a tenant with the loop on; outside an alert's
// conversation each refuses before reading or recording anything.
func TestAlertToolsRefuseOutsideAnAlertsConversation(t *testing.T) {
	const account = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	core.SetConversationDao(sessionDao{session: "C1-1700000000.1"})
	t.Cleanup(func() { core.SetConversationDao(nil) })
	withMetastore(t, nil)
	ctx := toolcore.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", []string{account}),
		AccountId: account, ConversationId: "conversation-1"}
	for name, input := range map[string]toolcore.NBToolCallRequest{
		toolcore.ToolGetRemediationPlan: {},
		toolcore.ToolRunStep:            {Command: "kubectl rollout restart deploy/api -n shop", Arguments: map[string]any{"plan_version": "v1", "step": 1, "action": "apply"}},
		toolcore.ToolProposeFix:         {Arguments: map[string]any{"findings": "f", "plan": `{"actions":[]}`}},
	} {
		tool, ok := toolcore.GetNBTool(account, name)
		require.True(t, ok, name)
		_, err := tool.Call(ctx, input)
		assert.ErrorContains(t, err, "not about an alert", name)
	}
}
