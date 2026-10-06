package api

import (
	"strings"
	"testing"
	"time"

	"nudgebee/llm/tools"

	"github.com/stretchr/testify/require"
)

func facts(provider string, connected bool, refusals map[string]executorRefusal) executorFacts {
	return executorFacts{provider: provider, connected: connected, refusals: refusals, now: time.Now().UTC()}
}

// An agent that cannot authenticate is never marked connected, so nothing NudgeBee sends reaches the
// cluster: the restart is the operator's to run.
func TestDisconnectedAgentMakesClusterCommandsTheOperators(t *testing.T) {
	restart := RemediationAction{Action: "Restart agent runner deployment", ExecuteCommand: "kubectl rollout restart deployment agent-runner -n nudgebee"}
	mode, reason, retry := deliveryFor(restart, facts("k8s", false, nil))
	require.Equal(t, DeliveryOperator, mode)
	require.Contains(t, reason, "agent is not connected")
	require.False(t, retry, "a command cannot reach a disconnected agent")

	mode, reason, _ = deliveryFor(restart, facts("k8s", true, nil))
	require.Equal(t, DeliveryRun, mode)
	require.Empty(t, reason)
}

// A verb the executor refused on permission grounds is shown with the executor's own words.
func TestRefusedVerbIsTheOperatorsWithTheExecutorsMessage(t *testing.T) {
	refusals := map[string]executorRefusal{
		"kubectl|set|deployment|shop":                      {Verb: "set", Message: `kubectl: verb "set" not in read-only allowlist`, RefusedAt: time.Now().UTC().Add(-10 * time.Minute)},
		"aws_execute|lambda update-function-configuration": {Verb: "lambda update-function-configuration", Message: "AccessDeniedException", RefusedAt: time.Now().UTC()},
	}
	mode, reason, retry := deliveryFor(RemediationAction{ExecuteCommand: "kubectl set resources deployment api -n shop --limits=memory=512Mi"}, facts("k8s", true, refusals))
	require.Equal(t, DeliveryOperator, mode)
	require.Contains(t, reason, "read-only allowlist")
	require.Contains(t, reason, "10 min ago")
	require.True(t, retry, "write permission may have been granted since the refusal")

	// Another verb on the same agent is unaffected.
	mode, _, _ = deliveryFor(RemediationAction{ExecuteCommand: "kubectl rollout restart deployment api -n shop"}, facts("k8s", true, refusals))
	require.Equal(t, DeliveryRun, mode)

	// Kubernetes RBAC is namespaced: the same verb in another namespace is unaffected.
	mode, _, _ = deliveryFor(RemediationAction{ExecuteCommand: "kubectl set resources deployment api -n billing --limits=memory=1Gi"}, facts("k8s", true, refusals))
	require.Equal(t, DeliveryRun, mode)

	// RBAC grants per resource: the same verb on another kind is unaffected, an alias of the same kind is not.
	mode, _, _ = deliveryFor(RemediationAction{ExecuteCommand: "kubectl set resources statefulset db -n shop --limits=memory=1Gi"}, facts("k8s", true, refusals))
	require.Equal(t, DeliveryRun, mode)
	mode, _, _ = deliveryFor(RemediationAction{ExecuteCommand: "kubectl set env deploy/api -n shop A=b"}, facts("k8s", true, refusals))
	require.Equal(t, DeliveryOperator, mode)
}

func TestSourceAndAlertRuleAndWrongProvider(t *testing.T) {
	mode, _, _ := deliveryFor(RemediationAction{RequiredChange: &RequiredChange{Target: "values.yaml", Change: "probe path /"}}, facts("k8s", true, nil))
	require.Equal(t, DeliverySource, mode)

	mode, _, _ = deliveryFor(RemediationAction{AlertRuleChange: &AlertRuleChange{Operation: "mute"}}, facts("k8s", false, nil))
	require.Equal(t, DeliveryRun, mode, "applied through NudgeBee's rule update, not the agent")

	mode, reason, retry := deliveryFor(RemediationAction{ExecuteCommand: "kubectl get pods -n shop"}, facts("aws", true, nil))
	require.Equal(t, DeliveryOperator, mode)
	require.Contains(t, reason, "can't run this command on this account")
	require.False(t, retry)
}

// "I ran this" binds to the plan like Run does: a command of the plan, or a command-less source change by name.
func TestOperatorStepIsPlanBound(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{
		{Action: "Update agent auth secret", RequiredChange: &RequiredChange{Target: "Secret agent-auth", Change: "registered key"}},
		{Action: "Restart agent runner", ExecuteCommand: "kubectl rollout restart deployment agent-runner -n nudgebee"},
	}}
	load := func() (RemediationPlan, bool) { return plan, true }
	ok := func(cmd, action string) string {
		return planCommandRejectionFor(RemediationExecuteRequest{EventId: "e", Slot: RemediationSlotManual, Command: cmd, Action: action}, load)
	}
	require.Empty(t, ok("kubectl rollout restart deployment agent-runner -n nudgebee", ""))
	require.Empty(t, ok("", "Update agent auth secret"))
	require.NotEmpty(t, ok("kubectl delete ns nudgebee", ""))
	require.NotEmpty(t, ok("", "Restart agent runner"), "an action with a command is marked by its command")
	require.Equal(t, "change:Update agent auth secret", operatorStepRef("", "Update agent auth secret"))
}

// Refusals come from the attempts every Run already records: the newest attempt per verb decides, a
// later success clears it, and a manual "I ran this" says nothing about NudgeBee's executor.
func TestRefusalsFromAttempts(t *testing.T) {
	now := time.Now().UTC()
	rows := []attemptRow{ // newest first
		{Command: "kubectl set resources deployment api -n shop --limits=memory=1Gi", Status: "Success", Manual: true, CreatedAt: now},
		{Command: "kubectl set env deployment api -n shop A=b", Status: "Failed", Error: `kubectl: verb "set" not in read-only allowlist`, CreatedAt: now.Add(-time.Minute)},
		{Command: "kubectl rollout restart deployment api -n shop", Status: "Success", CreatedAt: now.Add(-2 * time.Minute)},
		{Command: "kubectl rollout restart deployment web -n shop", Status: "Failed", Error: `Error from server (Forbidden): deployments.apps "web" is forbidden: User "u" cannot patch resource "deployments"`, CreatedAt: now.Add(-3 * time.Minute)},
		{Command: "aws lambda delete-function-concurrency --function-name f", Status: "Failed", Error: "agent connection lost", CreatedAt: now},
	}
	got := refusalsFromAttempts(rows, "k8s")
	require.Contains(t, got, "kubectl|set|deployment|shop", "the manual record is skipped, the refusal under it counts")
	require.NotContains(t, got, "kubectl|rollout|deployment|shop", "a later success clears an earlier refusal")
	require.Len(t, got, 1, "a lost connection is not a permission refusal")

	// A timeout after a refusal does not hide it: only a success clears one.
	masked := refusalsFromAttempts([]attemptRow{
		{Command: "kubectl set env deployment api -n shop A=c", Status: "Failed", Error: "context deadline exceeded", CreatedAt: now},
		{Command: "kubectl set env deployment api -n shop A=b", Status: "Failed", Error: `kubectl: verb "set" not in read-only allowlist`, CreatedAt: now.Add(-time.Minute)},
	}, "k8s")
	require.Contains(t, masked, "kubectl|set|deployment|shop")
}

// The cloud workspace puts the CLI's AccessDenied on stdout and only "exit status 254" in the error field.
func TestFailureTextKeepsTheCloudCLIsOwnWords(t *testing.T) {
	r := tools.RemediationExecutionResult{Success: false, ExitCode: 254, Stderr: "exit status 254",
		Error:  `cloud cli: aws_execute failed: workspace command failed: status="failed" error="exit status 254"`,
		Stdout: "[account: dev-aws]\n\nAn error occurred (AccessDenied) when calling the PutRolePolicy operation: User: arn:aws:sts::1:assumed-role/r is not authorized to perform: iam:PutRolePolicy"}
	require.True(t, isPermissionRefusal(failureText(r)))
	require.True(t, strings.HasPrefix(errorLine(failureText(r), isPermissionRefusal), "An error occurred (AccessDenied)"), "the reason quotes AWS's own sentence")
	require.Empty(t, failureText(tools.RemediationExecutionResult{Success: true, Stdout: "ok"}))
}

func TestIsPermissionRefusal(t *testing.T) {
	for _, refused := range []string{
		`kubectl: verb "set" not in read-only allowlist; enable runner.enableWritePermissions for writes`,
		`Error from server (Forbidden): deployments.apps "api" is forbidden: User "system:serviceaccount:x" cannot patch resource`,
		`An error occurred (AccessDeniedException) when calling the DeleteFunctionConcurrency operation: User: arn:aws:sts::1:assumed-role/r is not authorized to perform: lambda:DeleteFunctionConcurrency`,
		`ERROR: (gcloud.run.services.update) PERMISSION_DENIED: Permission 'run.services.update' denied`,
		`Error from server (Forbidden): User "system:serviceaccount:ns:sa" cannot patch resource "deployments" in API group "apps" in the namespace "shop"`,
	} {
		require.True(t, isPermissionRefusal(refused), refused)
	}
	for _, other := range []string{"agent connection lost", "relay error: agent not connected", "context deadline exceeded", "cluster agent returned status 503: overloaded", "error: deployments.apps \"api\" not found",
		`Error from server (Forbidden): pods "x" is forbidden: exceeded quota: q, requested: limits.memory=1Gi`,
		`Error from server (Forbidden): admission webhook "validation.gatekeeper.sh" denied the request: [required-labels] missing label team`,
	} {
		require.False(t, isPermissionRefusal(other), other)
	}
}

func TestExecutorVerb(t *testing.T) {
	require.Equal(t, "set", executorVerb("kubectl set resources deployment api -n shop --limits=memory=1Gi"))
	require.Equal(t, "rollout", executorVerb("kubectl rollout restart deployment api"))
	require.Equal(t, "upgrade", executorVerb("helm upgrade web ./chart -n demo --set a=b"))
	require.Equal(t, "lambda update-function-configuration", executorVerb("aws lambda update-function-configuration --function-name f --region us-east-1"))
	require.Equal(t, "run services update", executorVerb("gcloud run services update svc --region us-central1 --memory 1Gi"))
	require.Equal(t, "", executorVerb("kubectl"))
}
