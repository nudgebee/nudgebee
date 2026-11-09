package api

import (
	"testing"

	"nudgebee/llm/tools"

	"github.com/stretchr/testify/require"
)

// The check reads the event's own signal: the owning workload's rollout for a pod alert, the alarm's
// state for a CloudWatch alarm, nothing where no honest read exists.
func TestDeriveOutcomeCheck(t *testing.T) {
	pod := &eventFacts{SubjectType: "pod", SubjectName: "nbdemo-quota-55fb4994cc-k588g", SubjectNS: "nbdemo-quota", Owner: "nbdemo-quota", OwnerKind: "deployment"}
	c := deriveOutcomeCheck(pod, "k8s")
	require.Equal(t, "kubectl rollout status deployment/nbdemo-quota -n nbdemo-quota --timeout=90s", c.Command)
	require.Empty(t, c.Expect)
	require.True(t, isReadOnlyCommand(c.Command))
	require.Empty(t, remediationCommandRejection(c.Command))

	require.Nil(t, deriveOutcomeCheck(&eventFacts{SubjectType: "pod", SubjectName: "lonely", SubjectNS: "x"}, "k8s"), "a bare pod has no rollout")
	require.Equal(t, "kubectl rollout status statefulset/db -n x --timeout=90s",
		deriveOutcomeCheck(&eventFacts{SubjectType: "pod", SubjectName: "db-0", SubjectNS: "x", Owner: "db", OwnerKind: "StatefulSet.apps"}, "k8s").Command)
	require.Equal(t, "kubectl rollout status ds/agent -n x --timeout=90s",
		deriveOutcomeCheck(&eventFacts{SubjectType: "ds", SubjectName: "agent", SubjectNS: "x"}, "k8s").Command)

	alarm := &eventFacts{Labels: map[string]string{"aws_event_name": "nbdemo-orders-dlq-depth", "aws_event_arn": "arn:aws:cloudwatch:us-east-1:1:alarm:nbdemo-orders-dlq-depth", "aws_region": "us-east-1"}}
	c = deriveOutcomeCheck(alarm, "aws")
	require.Equal(t, "aws cloudwatch describe-alarms --alarm-names nbdemo-orders-dlq-depth --region us-east-1 --query 'MetricAlarms[0].StateValue' --output text", c.Command)
	require.Equal(t, "OK", c.Expect)
	require.Empty(t, remediationCommandRejection(c.Command))

	require.Nil(t, deriveOutcomeCheck(&eventFacts{SubjectType: "cloud_run_revision", SubjectName: "svc"}, "gcp"), "no honest read of a GCP incident")

	spaced := deriveOutcomeCheck(&eventFacts{Labels: map[string]string{"aws_event_name": "High CPU - prod", "aws_event_arn": "arn:aws:cloudwatch:us-east-1:1:alarm:High CPU - prod", "aws_region": "us-east-1"}}, "aws")
	require.Contains(t, spaced.Command, "--alarm-names 'High CPU - prod'", "an alarm name with spaces stays one argument")
	require.Empty(t, remediationCommandRejection(spaced.Command))
}

func TestOutcomePassedIsThreeValued(t *testing.T) {
	rollout := &OutcomeCheck{Command: "kubectl rollout status deployment/x -n y --timeout=90s"}
	require.True(t, *outcomePassed(rollout, tools.RemediationExecutionResult{Success: true}, true))
	require.False(t, *outcomePassed(rollout, tools.RemediationExecutionResult{Success: false, ExitCode: 1}, true))
	require.Nil(t, outcomePassed(rollout, tools.RemediationExecutionResult{Success: true}, false), "no exit code, no verdict")
	require.Nil(t, outcomePassed(rollout, tools.RemediationExecutionResult{ExitCode: 1, Stderr: `Error from server (NotFound): deployments.apps "x" not found`}, true), "workload gone")
	require.Nil(t, outcomePassed(rollout, tools.RemediationExecutionResult{ExitCode: 1, Stderr: `Error from server (Forbidden): deployments.apps "x" is forbidden: User "agent" cannot get`}, true), "agent may not read it")
	require.False(t, *outcomePassed(rollout, tools.RemediationExecutionResult{ExitCode: 1, Stderr: "error: timed out waiting for the condition"}, true))

	alarm := &OutcomeCheck{Expect: "OK"}
	require.True(t, *outcomePassed(alarm, tools.RemediationExecutionResult{Success: true, Stdout: "OK\n"}, true))
	require.False(t, *outcomePassed(alarm, tools.RemediationExecutionResult{Success: true, Stdout: "ALARM"}, true))
	require.Nil(t, outcomePassed(alarm, tools.RemediationExecutionResult{Success: false, Stderr: "AccessDenied"}, true))
	require.Nil(t, outcomePassed(alarm, tools.RemediationExecutionResult{Success: true, Stdout: "None"}, true), "no alarm matched is not a verdict")
	require.Nil(t, outcomePassed(alarm, tools.RemediationExecutionResult{Success: true, Stdout: "INSUFFICIENT_DATA"}, true), "no data is not a verdict")
	// E2E on dev: the cloud workspace prints an "[account: x]" banner above the answer.
	require.True(t, *outcomePassed(alarm, tools.RemediationExecutionResult{Success: true, Stdout: "[account: dev-aws]\n\nOK\n"}, true))
	require.False(t, *outcomePassed(alarm, tools.RemediationExecutionResult{Success: true, Stdout: "[account: dev-aws]\nALARM"}, true))
}

func TestOutcomeCheckIsPlanBound(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{{ExecuteCommand: "kubectl set resources deployment x -n y --limits=memory=1Gi"}},
		OutcomeCheck: &OutcomeCheck{Command: "kubectl rollout status deployment/x -n y --timeout=90s"}}
	load := func() (RemediationPlan, bool) { return plan, true }
	require.Empty(t, planCommandRejectionFor(RemediationExecuteRequest{EventId: "e", Slot: RemediationSlotOutcome, Command: plan.OutcomeCheck.Command}, load))
	require.NotEmpty(t, planCommandRejectionFor(RemediationExecuteRequest{EventId: "e", Slot: RemediationSlotOutcome, Command: "kubectl get secrets -A"}, load))
}
