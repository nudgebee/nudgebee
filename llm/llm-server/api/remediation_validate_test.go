package api

import (
	"testing"

	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func act(kind string, conf Confidence, exec, verify, rollback string) RemediationAction {
	return RemediationAction{
		Hypothesis: "h", Action: "a", Kind: kind, Title: "t", Confidence: conf,
		ExecuteCommand: exec, VerifyCommand: verify, RollbackCommand: rollback,
	}
}

func planWith(rootCause string, actions ...RemediationAction) *RemediationPlan {
	return &RemediationPlan{RootCause: rootCause, Summary: "s", Actions: actions}
}

// TestCommandHasPlaceholder covers the exact strings the audit found being offered as runnable
// fixes, including the production one.
func TestCommandHasPlaceholder(t *testing.T) {
	for _, c := range []string{
		"kubectl set env deployment/app-prod -n nudgebee GITHUB_WEBHOOK_SECRET=GITHUB_WEBHOOK_SECRET_PLACEHOLDER",
		"kubectl create secret docker-registry ghcr-secret --docker-username=GITHUB_USER_PLACEHOLDER",
		"kubectl set env deployment api -n prod DB_PASSWORD=YOUR_PASSWORD_HERE",
		"kubectl scale deployment <name> -n prod --replicas 3",
		"kubectl set env deployment api -n prod TOKEN=CHANGEME",
	} {
		assert.True(t, commandHasPlaceholder(c), "should be flagged: %s", c)
	}
	for _, c := range []string{
		"kubectl set resources deployment rag-server -n nudgebee --limits=memory=1Gi",
		"kubectl rollout undo deployment app-dev -n nudgebee",
		"aws ec2 reboot-instances --instance-ids i-00e845d10772a9058 --region us-east-1",
	} {
		assert.False(t, commandHasPlaceholder(c), "should not be flagged: %s", c)
	}
}

// TestPlaceholderActionIsDemotedAndStripped: the plan may keep the command as guidance, but it must
// not be the ranked-first durable answer, and nothing may hang off it.
func TestPlaceholderActionIsDemotedAndStripped(t *testing.T) {
	p := planWith("secret mismatch after rollout",
		act(RemediationKindFix, 85,
			"kubectl set env deployment/app-prod -n nudgebee GITHUB_WEBHOOK_SECRET=GITHUB_WEBHOOK_SECRET_PLACEHOLDER",
			"kubectl rollout status deployment/app-prod -n nudgebee",
			"kubectl rollout undo deployment/app-prod -n nudgebee"))
	notes := validatePlanCommands(p)
	require.Len(t, p.Actions, 1)
	assert.Equal(t, RemediationKindMitigation, p.Actions[0].Kind)
	assert.LessOrEqual(t, int(p.Actions[0].Confidence), int(placeholderMaxConfidence))
	assert.Empty(t, p.Actions[0].VerifyCommand, "nothing to verify: the command will be refused")
	assert.Empty(t, p.Actions[0].RollbackCommand)
	assert.Contains(t, ruleNames(notes), "placeholder_demoted_from_fix")
}

// TestDependentActionOnBlockedStepIsDemoted: attaching a secret that could not be created, then
// restarting to pick it up, is individually well-formed and collectively makes the outage worse.
func TestDependentActionOnBlockedStepIsDemoted(t *testing.T) {
	p := planWith("missing imagePullSecrets for ghcr.io",
		act(RemediationKindFix, 85, "kubectl create secret docker-registry ghcr-secret --docker-server=ghcr.io --docker-username=GITHUB_USER_PLACEHOLDER -n qa", "", ""),
		act(RemediationKindFix, 80, `kubectl patch serviceaccount llm-gateway -n qa -p '{"imagePullSecrets":[{"name":"ghcr-secret"}]}'`, "", ""))
	notes := validatePlanCommands(p)
	require.Len(t, p.Actions, 2)
	assert.Equal(t, RemediationKindMitigation, p.Actions[1].Kind)
	assert.Contains(t, ruleNames(notes), "depends_on_blocked_action_demoted")
}

// TestReadOnlyActionIsDropped: `kubectl get pods` shipped as an action at confidence 50.
func TestReadOnlyActionIsDropped(t *testing.T) {
	p := planWith("memory limit too low",
		act(RemediationKindMitigation, 50, "kubectl set resources deployment relay-server -n nudgebee --limits=memory=512Mi", "", ""),
		act(RemediationKindMitigation, 50, "kubectl get pods -n nudgebee -l app=relay-server", "kubectl get pods -n nudgebee -l app=relay-server", ""))
	validatePlanCommands(p)
	require.Len(t, p.Actions, 1)
	assert.Equal(t, "kubectl set resources deployment relay-server -n nudgebee --limits=memory=512Mi", p.Actions[0].ExecuteCommand)
}

// TestVerifyReadsBackTheValue: `rollout status` passes for whatever value was set, including a wrong
// one. The single passing verify in the whole audit was exactly this shape.
func TestVerifyReadsBackTheValue(t *testing.T) {
	cases := []struct{ exec, wantVerify string }{
		{"kubectl set resources deployment sessions-api -n sessions --limits=memory=1Gi --requests=memory=512Mi",
			"kubectl get deployment sessions-api -n sessions -o jsonpath='{.spec.template.spec.containers[*].resources}'"},
		{"kubectl set env statefulset zookeeper -n pinot ZOO_AUTOPURGE_PURGEINTERVAL=24",
			"kubectl get statefulset zookeeper -n pinot -o jsonpath='{.spec.template.spec.containers[*].env}'"},
		{"kubectl scale statefulset redis-replicas -n redis --replicas 1",
			"kubectl get statefulset redis-replicas -n redis -o jsonpath='{.spec.replicas}'"},
		{"kubectl set resources deployment/report-worker -n ns-232 --limits=memory=512Mi",
			"kubectl get deployment report-worker -n ns-232 -o jsonpath='{.spec.template.spec.containers[*].resources}'"},
	}
	for _, c := range cases {
		p := planWith("limit too low", act(RemediationKindFix, 90, c.exec, "kubectl rollout status deployment x -n y", ""))
		validatePlanCommands(p)
		assert.Equal(t, c.wantVerify, p.Actions[0].VerifyCommand, "for %s", c.exec)
	}
}

// TestDerivedVerifySurvivesTheExecuteGuards: a verify the panel cannot run is no better than none.
func TestDerivedVerifySurvivesTheExecuteGuards(t *testing.T) {
	v := deriveVerify("kubectl set resources deployment sessions-api -n sessions --limits=memory=1Gi")
	require.NotEmpty(t, v)
	assert.False(t, containsShellMetacharacters(v), "must pass the metacharacter guard")
	assert.False(t, isStructurallyTruncated(v), "must pass the balanced-delimiter guard")
	assert.True(t, isReadOnlyCommand(v), "a verify must be read-only")
}

// TestSelectorVerifyIsNotAccepted: prints nothing when the fix held and nothing when the selector is
// wrong, so it cannot fail.
func TestSelectorVerifyIsNotAccepted(t *testing.T) {
	p := planWith("limit too low",
		act(RemediationKindFix, 90, "kubectl set resources deployment report-worker -n ns --limits=memory=512Mi",
			"kubectl get pod -n ns -l target_service=report-worker", ""))
	validatePlanCommands(p)
	assert.NotContains(t, p.Actions[0].VerifyCommand, "-l ")
	assert.Contains(t, p.Actions[0].VerifyCommand, "jsonpath")
}

// TestKubectlExecVerifyIsKeptWhenTheRemoteCommandOnlyReports: `-- df -h` is a real verification.
func TestKubectlExecVerifyIsKeptWhenTheRemoteCommandOnlyReports(t *testing.T) {
	assert.True(t, isReadOnlyCommand("kubectl exec zookeeper-0 -n pinot -- df -h /datalog"))
	assert.False(t, isReadOnlyCommand("kubectl exec zookeeper-0 -n pinot -- zkCleanup.sh /datalog 3"))
	assert.False(t, isReadOnlyCommand("kubectl exec pod -n ns -- rm -rf /data"))
}

// TestRollbackForRestartIsCleared: `rollout undo` is not the inverse of `rollout restart` — it
// reverts the previous real deployment. 16 plans offered exactly this.
func TestRollbackForRestartIsCleared(t *testing.T) {
	p := planWith("stale connections",
		act(RemediationKindMitigation, 45, "kubectl rollout restart deployment relay-server -n nudgebee",
			"kubectl rollout status deployment relay-server -n nudgebee",
			"kubectl rollout undo deployment relay-server -n nudgebee"))
	notes := validatePlanCommands(p)
	assert.Empty(t, p.Actions[0].RollbackCommand, "a restart changes no desired state, so it needs no undo")
	assert.Contains(t, ruleNames(notes), "rollback_for_restart_cleared")
}

// TestRollbackIdenticalToExecuteIsCleared: `rollout undo` undone by `rollout undo` reinstates the
// broken revision.
func TestRollbackIdenticalToExecuteIsCleared(t *testing.T) {
	p := planWith("bad image tag",
		act(RemediationKindMitigation, 50, "kubectl rollout undo deployment notifications -n test",
			"kubectl rollout status deployment notifications -n test",
			"kubectl rollout undo deployment notifications -n test"))
	validatePlanCommands(p)
	assert.Empty(t, p.Actions[0].RollbackCommand)
}

// TestDestructiveRollbackIsCleared: the audit found "start PostgreSQL" whose Undo button was
// `systemctl stop postgresql`.
func TestDestructiveRollbackIsCleared(t *testing.T) {
	p := planWith("upstream database stopped by a test script",
		act(RemediationKindFix, 90,
			`aws ssm send-command --instance-ids "i-01" --document-name "AWS-RunShellScript" --parameters 'commands=["systemctl start postgresql"]' --region us-east-1`,
			"aws cloudwatch describe-alarms --alarm-names x --region us-east-1",
			`aws ssm send-command --instance-ids "i-01" --document-name "AWS-RunShellScript" --parameters 'commands=["systemctl stop postgresql"]' --region us-east-1`))
	notes := validatePlanCommands(p)
	assert.Empty(t, p.Actions[0].RollbackCommand)
	assert.Contains(t, ruleNames(notes), "destructive_rollback_cleared")
}

// TestRestartAgainstAResistantCauseCannotBeAFix: the pod comes back and fails on the same
// credential, the same denied permission, the same full disk.
func TestRestartAgainstAResistantCauseCannotBeAFix(t *testing.T) {
	for _, cause := range []string{
		"IAM user is blocked by an explicit deny in the EnforceMFA policy",
		"the agent is misconfigured with an invalid authentication token",
		"the persistent volume is completely out of disk space, 0 B free",
		"lookup k8s-collector.qa.svc: no such host",
	} {
		p := planWith(cause, act(RemediationKindFix, 90, "kubectl rollout restart deployment api -n prod", "", ""))
		validatePlanCommands(p)
		assert.Equal(t, RemediationKindMitigation, p.Actions[0].Kind, "cause: %s", cause)
		assert.LessOrEqual(t, int(p.Actions[0].Confidence), int(restartAgainstResistantCauseMaxConfidence))
	}
}

// TestRestartIsKeptForEphemeralStorage: recreating a pod really does recreate a memory-backed
// emptyDir, so the disk-full rule must not demote it.
func TestRestartIsKeptForEphemeralStorage(t *testing.T) {
	p := planWith("the cache-dumper sidecar filled the 32Mi memory-backed emptyDir at /data, no space left on device",
		act(RemediationKindMitigation, 45, "kubectl rollout restart deployment cart-cache -n shop", "", ""))
	validatePlanCommands(p)
	assert.Equal(t, Confidence(45), p.Actions[0].Confidence, "a restart does clear an emptyDir")
}

// TestContainerLimitIsNotTheApplicationSetting: Redis thrashing at maxmemory is not fixed by giving
// the container more room.
func TestContainerLimitIsNotTheApplicationSetting(t *testing.T) {
	p := planWith("redis-cache saturated its 1048 MB maxmemory ceiling, triggering eviction thrashing",
		act(RemediationKindFix, 80, "kubectl set resources deployment redis-cache -n ns --limits=memory=2Gi", "", ""))
	notes := validatePlanCommands(p)
	assert.Equal(t, RemediationKindMitigation, p.Actions[0].Kind)
	assert.Contains(t, ruleNames(notes), "container_limit_is_not_the_app_setting_demoted")
}

// TestGoodPlansAreLeftAlone guards against the checks eating correct output.
func TestGoodPlansAreLeftAlone(t *testing.T) {
	p := planWith("the report-worker container memory limit is 192Mi against a 220MB startup buffer",
		act(RemediationKindFix, 90,
			"kubectl set resources deployment report-worker -n ns-232 --limits=memory=512Mi --requests=memory=256Mi",
			"kubectl get deployment report-worker -n ns-232 -o jsonpath='{.spec.template.spec.containers[*].resources}'",
			"kubectl set resources deployment report-worker -n ns-232 --limits=memory=192Mi --requests=memory=128Mi"))
	notes := validatePlanCommands(p)
	assert.Empty(t, notes, "a correct action must pass untouched")
	assert.Equal(t, RemediationKindFix, p.Actions[0].Kind)
	assert.Equal(t, Confidence(90), p.Actions[0].Confidence)
}

// TestHistoricalPlans replays the real plans that exposed each defect and asserts the contract now
// holds across all of them. The corpus is the generated output as it was actually shipped.
func ruleNames(notes []planValidationNote) []string {
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		out = append(out, n.Rule)
	}
	return out
}

func TestUnobservantVerifyIsReplacedWithAValueRead(t *testing.T) {
	// `kubectl describe` is read-only, so it survives the read-only gate, and it exits 0 whatever
	// the field says. It is the same defect as `rollout status`, wearing a different command.
	plan := RemediationPlan{Actions: []RemediationAction{{
		Action:         "raise memory",
		ExecuteCommand: "kubectl set resources deployment rag-server -n nudgebee --limits=memory=1.5Gi --requests=memory=1Gi",
		VerifyCommand:  "kubectl describe deployment rag-server -n nudgebee",
	}}}
	notes := validatePlanCommands(&plan)
	assert.Contains(t, ruleNames(notes), "verify_does_not_observe_state_replaced")
	assert.Equal(t,
		"kubectl get deployment rag-server -n nudgebee -o jsonpath='{.spec.template.spec.containers[*].resources}'",
		plan.Actions[0].VerifyCommand)
}

func TestLogsVerifyIsClearedWhenNothingCanBeDerived(t *testing.T) {
	// Logs report what the workload printed, not what the change set. With no derivable value read
	// the honest result is no verify: one that cannot fail is read as proof.
	plan := RemediationPlan{Actions: []RemediationAction{{
		Action:         "remove the stale ingress",
		ExecuteCommand: "kubectl delete ingress gha-metrics-webhook -n nudgebee",
		VerifyCommand:  "kubectl logs deployment/ingress-nginx-controller -n ingress-nginx --tail=50",
	}}}
	notes := validatePlanCommands(&plan)
	assert.Contains(t, ruleNames(notes), "verify_does_not_observe_state_cleared")
	assert.Empty(t, plan.Actions[0].VerifyCommand)
}

func TestIncompleteRollbackIsReplacedWithRevisionUndo(t *testing.T) {
	// Restoring limits but not requests leaves the workload in a third state: neither the original
	// nor the fix. `rollout undo` reverts the whole revision the `set` created.
	plan := RemediationPlan{Actions: []RemediationAction{{
		Action:          "raise memory",
		ExecuteCommand:  "kubectl set resources deployment sessions-api -n sessions --limits=memory=1Gi --requests=memory=512Mi",
		RollbackCommand: "kubectl set resources deployment sessions-api -n sessions --limits=memory=192Mi",
	}}}
	notes := validatePlanCommands(&plan)
	assert.Contains(t, ruleNames(notes), "rollback_incomplete_replaced_with_revision_undo")
	assert.Equal(t, "kubectl rollout undo deployment sessions-api -n sessions", plan.Actions[0].RollbackCommand)
}

func TestIncompleteEnvRollbackIsReplaced(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{{
		Action:          "enable autopurge",
		ExecuteCommand:  "kubectl set env statefulset zookeeper -n pinot ZOO_AUTOPURGE_PURGEINTERVAL=24 ZOO_AUTOPURGE_SNAPRETAINCOUNT=3",
		RollbackCommand: "kubectl set env statefulset zookeeper -n pinot ZOO_AUTOPURGE_PURGEINTERVAL=0",
	}}}
	validatePlanCommands(&plan)
	assert.Equal(t, "kubectl rollout undo statefulset zookeeper -n pinot", plan.Actions[0].RollbackCommand)
}

func TestCompleteRollbackIsLeftAlone(t *testing.T) {
	// Every key the execute wrote is restored, so there is nothing to correct.
	plan := RemediationPlan{Actions: []RemediationAction{{
		Action:          "raise memory",
		ExecuteCommand:  "kubectl set resources deployment web -n shop --limits=memory=1Gi --requests=memory=512Mi",
		RollbackCommand: "kubectl set resources deployment web -n shop --limits=memory=256Mi --requests=memory=128Mi",
	}}}
	notes := validatePlanCommands(&plan)
	assert.NotContains(t, ruleNames(notes), "rollback_incomplete_replaced_with_revision_undo")
	assert.Equal(t,
		"kubectl set resources deployment web -n shop --limits=memory=256Mi --requests=memory=128Mi",
		plan.Actions[0].RollbackCommand)
}

func TestRolloutUndoCountsAsACompleteInverse(t *testing.T) {
	// `set resources` creates a revision, so undoing it restores both fields. The model gets this
	// right in two corpus plans and must not be second-guessed.
	plan := RemediationPlan{Actions: []RemediationAction{{
		Action:          "raise memory",
		ExecuteCommand:  "kubectl set resources deployment temporal-history -n qa --requests=memory=1Gi --limits=memory=2Gi",
		RollbackCommand: "kubectl rollout undo deployment temporal-history -n qa",
	}}}
	notes := validatePlanCommands(&plan)
	assert.NotContains(t, ruleNames(notes), "rollback_incomplete_replaced_with_revision_undo")
	assert.Equal(t, "kubectl rollout undo deployment temporal-history -n qa", plan.Actions[0].RollbackCommand)
}

func TestIncompleteRollbackOnAnUnversionedKindIsClearedNotSubstituted(t *testing.T) {
	// `rollout undo` only reverts kinds with a revision history. Substituting it for anything else
	// would revert something unrelated, so the rollback is dropped instead.
	plan := RemediationPlan{Actions: []RemediationAction{{
		Action:          "set env on a job",
		ExecuteCommand:  "kubectl set env cronjob nightly -n ops A=1 B=2",
		RollbackCommand: "kubectl set env cronjob nightly -n ops A=0",
	}}}
	notes := validatePlanCommands(&plan)
	assert.Contains(t, ruleNames(notes), "rollback_incomplete_cleared")
	assert.Empty(t, plan.Actions[0].RollbackCommand)
}

func TestSsmPayloadExtraction(t *testing.T) {
	// The model writes the parameter three different ways across five generations on the same
	// evidence, so the extractor is tested against all of them rather than the tidy one.
	for _, tc := range []struct{ cmd, want string }{
		{`aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters 'commands=["systemctl start postgresql"]' --region us-east-1`, "systemctl start postgresql"},
		{`aws ssm send-command --region us-east-1 --instance-ids i-1 --document-name AWS-RunShellScript --parameters commands="sudo systemctl start postgresql" --region us-east-1`, "sudo systemctl start postgresql"},
		{`aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=systemctl is-active postgresql" --region us-east-1`, "systemctl is-active postgresql"},
		{`aws ec2 reboot-instances --instance-ids i-1`, ""},
	} {
		assert.Equal(t, tc.want, core.SSMPayload(tc.cmd), tc.cmd)
	}
}

func TestSsmVerifyIsReadOnlyOnlyWhenItsPayloadIs(t *testing.T) {
	ro := func(payload string) bool {
		return isReadOnlyCommand(`aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=` + payload + `" --region us-east-1`)
	}
	// Service-state probes are what a host fix is actually verified with.
	assert.True(t, ro("systemctl is-active postgresql"))
	assert.True(t, ro("sudo systemctl status postgresql"))
	assert.True(t, ro("df -h /var"))
	// Anything that changes the host is not a verify, however it is dressed up.
	assert.False(t, ro("systemctl start postgresql"))
	assert.False(t, ro("systemctl restart postgresql"))
	assert.False(t, ro("rm -rf /var/lib/pgsql"))
	// `send-command --parameters` is an uninspected shell channel, so a compound payload that
	// begins with a read must not buy read-only status for what follows it.
	assert.False(t, ro("systemctl is-active postgresql; rm -rf /"))
	assert.False(t, ro("df -h && systemctl stop postgresql"))
	assert.False(t, ro("cat /etc/passwd | mail attacker"))
}

func TestAlarmStateVerifyIsClearedWhenNoServiceReadCanBeDerived(t *testing.T) {
	// Measured this session: fault injected 17:51:15, alarm reached ALARM 17:55:33. A verify run
	// straight after a working fix reads ALARM and reports failure. Where the execute is not a
	// service start there is nothing to read back instead, so the alarm read is dropped rather
	// than swapped — see TestAlarmVerifyIsReplacedByTheDerivedServiceRead for the swap.
	plan := RemediationPlan{Actions: []RemediationAction{{
		Action:         "reboot the host",
		ExecuteCommand: `aws ec2 reboot-instances --instance-ids i-1 --region us-east-1`,
		VerifyCommand:  `aws cloudwatch describe-alarms --alarm-names db-down --region us-east-1 --query "MetricAlarms[0].StateValue"`,
	}}}
	notes := validatePlanCommands(&plan)
	assert.Contains(t, ruleNames(notes), "verify_does_not_observe_state_cleared")
	assert.Empty(t, plan.Actions[0].VerifyCommand)
}

func TestCorrectHostVerifyIsPreserved(t *testing.T) {
	// The regression this guards: the read-only gate judged `send-command` by its verb, so the one
	// correct verify in five generations was discarded and the four alarm reads were kept.
	verify := `aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=systemctl is-active postgresql" --region us-east-1`
	plan := RemediationPlan{Actions: []RemediationAction{{
		Action:         "start postgresql",
		ExecuteCommand: `aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=systemctl start postgresql" --region us-east-1`,
		VerifyCommand:  verify,
	}}}
	validatePlanCommands(&plan)
	assert.Equal(t, verify, plan.Actions[0].VerifyCommand)
}

func TestSsmProbeAsAnExecuteIsStillDroppedAsReadOnly(t *testing.T) {
	// Widening read-only must widen the action drop too: a plan whose "remediation" only probes
	// the host is a button that reports success without having done anything.
	plan := RemediationPlan{Actions: []RemediationAction{{
		Action:         "check postgres",
		ExecuteCommand: `aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=systemctl is-active postgresql" --region us-east-1`,
	}}}
	notes := validatePlanCommands(&plan)
	assert.Contains(t, ruleNames(notes), "read_only_action_dropped")
	assert.Empty(t, plan.Actions)
}

func TestDerivedSsmVerifyReadsTheServiceBack(t *testing.T) {
	got := deriveSsmVerify(`aws ssm send-command --instance-ids "i-0150dbd583caa0e69" --document-name "AWS-RunShellScript" --parameters 'commands=["systemctl start postgresql"]' --region us-east-1`)
	assert.Equal(t,
		`aws ssm send-command --instance-ids i-0150dbd583caa0e69 --document-name AWS-RunShellScript --parameters "commands=systemctl is-active postgresql" --region us-east-1`,
		got)
	// The derived verify must itself pass the read-only gate, or the next layer discards it.
	assert.True(t, isReadOnlyCommand(got))
}

func TestDerivedSsmVerifyRefusesAnythingItCannotBound(t *testing.T) {
	// Nothing free-form may reach `--parameters`, which the metacharacter guard does not inspect.
	for _, ex := range []string{
		// Compound payload: the unit token would carry the rest of the shell line.
		`aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=systemctl start postgresql; rm -rf /"`,
		// Not a service start.
		`aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=rm -rf /var/lib/pgsql"`,
		// Not SSM at all.
		`aws ec2 reboot-instances --instance-ids i-1 --region us-east-1`,
		// No instance to target.
		`aws ssm send-command --targets Key=tag:Role,Values=db --document-name AWS-RunShellScript --parameters "commands=systemctl start postgresql"`,
	} {
		assert.Empty(t, deriveSsmVerify(ex), ex)
	}
}

func TestAlarmVerifyIsReplacedByTheDerivedServiceRead(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{{
		Action:         "start postgresql",
		ExecuteCommand: `aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=systemctl start postgresql" --region us-east-1`,
		VerifyCommand:  `aws cloudwatch describe-alarms --alarm-names db-down --region us-east-1 --query "MetricAlarms[0].StateValue"`,
	}}}
	notes := validatePlanCommands(&plan)
	assert.Contains(t, ruleNames(notes), "verify_does_not_observe_state_replaced")
	assert.Contains(t, plan.Actions[0].VerifyCommand, "is-active postgresql")
}

func TestSettingsWrittenHandlesBooleanFlags(t *testing.T) {
	// `--overwrite` takes no value, so the KEY=value after it must still be counted. Skipping a
	// token after every flag would drop it and let a rollback that restores nothing look complete.
	assert.Equal(t, []string{"CACHE_TTL"},
		settingsWritten("kubectl set env deployment/app -n ns --overwrite CACHE_TTL=60"))
	// `-c` does take one.
	assert.Equal(t, []string{"A"},
		settingsWritten("kubectl set env deployment app -n ns -c web A=1"))
	// Order follows the command, which is irrelevant: the caller compares these as a set.
	assert.ElementsMatch(t, []string{"limits.cpu", "limits.memory", "requests.memory"},
		settingsWritten("kubectl set resources deployment app -n ns --limits=memory=2Gi,cpu=1 --requests=memory=1Gi"))
}

func TestResistantCauseMatchesHowCausesAreActuallyWritten(t *testing.T) {
	// The stems were only ever tested against tidy singular phrasing. A plain trailing `\b`
	// rejects the plural and the past tense, and treats `_` as a word character — so the three
	// most common real spellings of an auth failure all missed the rule.
	for _, s := range []string{
		"database credentials are invalid",
		"AUTHENTICATION_FAILED returned by ClickHouse",
		"ACCESS_DENIED from the registry",
		"missing permissions for the service account",
		"disk space exhausted",
		"the auth token has expired",
		"invalid credential supplied",
		"no space left on device",
	} {
		assert.True(t, causeResistsRestart.MatchString(s), "should be recognised as restart-resistant: %q", s)
	}
	// Suffixes are enumerated and the leading boundary kept, so a stem cannot swallow a longer
	// unrelated word.
	for _, s := range []string{
		"the secretary service was updated",
		"flights to Miami were delayed",
		"memory limit too low for the workload",
		"connection slots exhausted by long-running queries",
		"an external fast shutdown signal stopped the service",
	} {
		assert.False(t, causeResistsRestart.MatchString(s), "must not be treated as restart-resistant: %q", s)
	}
}

func TestRestartRuleAppliesOnBothSubstrates(t *testing.T) {
	// The rule is about what a restart can achieve, which does not depend on the CLI that delivers
	// it. The implementation was kubectl-only, so the identical action expressed against EC2 —
	// the substrate every host-based remediation and the whole live benchmark use — escaped it.
	cause := "The service is failing because its database credentials are invalid (AUTHENTICATION_FAILED)."
	for _, cmd := range []string{
		"kubectl rollout restart deployment db-client -n prod",
		`aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=systemctl restart postgresql" --region us-east-1`,
		`aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=sudo service postgresql restart" --region us-east-1`,
		"aws ec2 reboot-instances --instance-ids i-1 --region us-east-1",
	} {
		assert.True(t, restartLikeCommand(cmd), "should count as a restart: %s", cmd)
		plan := RemediationPlan{RootCause: cause, Actions: []RemediationAction{{
			Action: "cycle it", Kind: RemediationKindFix, Confidence: 90, ExecuteCommand: cmd,
		}}}
		notes := validatePlanCommands(&plan)
		assert.Contains(t, ruleNames(notes), "restart_cannot_fix_this_cause_demoted", cmd)
		assert.Equal(t, RemediationKindMitigation, plan.Actions[0].Kind, cmd)
		assert.Equal(t, restartAgainstResistantCauseMaxConfidence, plan.Actions[0].Confidence, cmd)
	}
}

func TestStartingAStoppedServiceIsNotARestart(t *testing.T) {
	// `systemctl start` on a stopped service is the correct fix, not a blunt cycle. Folding it in
	// would cap the one scenario where it is the right answer, so it stays out.
	start := `aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=systemctl start postgresql" --region us-east-1`
	assert.False(t, restartLikeCommand(start))
	// And a read-only probe over the same channel is not a restart either.
	probe := `aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=systemctl is-active postgresql" --region us-east-1`
	assert.False(t, restartLikeCommand(probe))
}

func TestCloudRestartAgainstAnOrdinaryCauseIsUntouched(t *testing.T) {
	// Widening the substrate must not demote restarts that are legitimate mitigations. A blocked
	// database session pool is cleared by a restart, and the ground truth calls that acceptable.
	plan := RemediationPlan{
		RootCause: "30 concurrent blocking queries saturated PostgreSQL connection capacity.",
		Actions: []RemediationAction{{
			Action: "restart postgresql", Kind: RemediationKindMitigation, Confidence: 45,
			ExecuteCommand: `aws ssm send-command --instance-ids i-1 --document-name AWS-RunShellScript --parameters "commands=systemctl restart postgresql" --region us-east-1`,
		}}}
	notes := validatePlanCommands(&plan)
	assert.NotContains(t, ruleNames(notes), "restart_cannot_fix_this_cause_demoted")
	assert.Equal(t, Confidence(45), plan.Actions[0].Confidence)
}

// The prompt asks for a second action that reprocesses work stranded while the fault lasted. The
// validator has never seen one, so this guards the shape end to end: the recovery action must
// survive validation intact, with its read-only verify kept and no rollback invented for something
// that has no inverse.
func TestRecoveringStrandedWorkSurvivesValidation(t *testing.T) {
	plan := &RemediationPlan{
		Hypotheses: []RemediationHypothesis{{Hypothesis: "consumer throttled to zero", Confidence: 95}},
		Actions: []RemediationAction{
			{
				Hypothesis:      "consumer throttled to zero",
				Action:          "Delete reserved concurrency",
				Kind:            RemediationKindFix,
				Confidence:      95,
				ExecuteCommand:  "aws lambda delete-function-concurrency --function-name invoice-processor --region us-east-1",
				VerifyCommand:   "aws lambda get-function-concurrency --function-name invoice-processor --region us-east-1",
				RollbackCommand: "aws lambda put-function-concurrency --function-name invoice-processor --reserved-concurrent-executions 0 --region us-east-1",
			},
			{
				Hypothesis:     "consumer throttled to zero",
				Action:         "Redrive dead-lettered invoices",
				Kind:           RemediationKindFix,
				Confidence:     80,
				ExecuteCommand: "aws sqs start-message-move-task --source-arn arn:aws:sqs:us-east-1:123456789012:invoice-events-dlq --region us-east-1",
				VerifyCommand:  "aws sqs list-message-move-tasks --source-arn arn:aws:sqs:us-east-1:123456789012:invoice-events-dlq --region us-east-1",
			},
		},
	}
	validatePlanCommands(plan)

	require.Len(t, plan.Actions, 2, "the recovery action must not be dropped")
	recovery := plan.Actions[1]
	require.Contains(t, recovery.ExecuteCommand, "start-message-move-task")
	require.Contains(t, recovery.VerifyCommand, "list-message-move-tasks", "a read-only task status check is a valid verify")
	require.Empty(t, recovery.RollbackCommand, "moving messages back is not an inverse; none should be invented")
}

// A plan command the Run button's executor would refuse must not reach the panel. The unquoted
// jsonpath is the shape the prompt itself used to teach; the relay rejects its bare braces.
func TestExecutorRejectedCommandsAreRemovedFromPlan(t *testing.T) {
	exec := "kubectl scale deployment web -n demo --replicas 3"
	p := planWith("replicas too low",
		act("fix", 80, exec, "kubectl get deployment web -n demo -o jsonpath={.spec.replicas}", "kubectl scale deployment web -n demo --replicas={2}"),
		act("fix", 70, `kubectl patch deployment web -n demo -p {"spec":{"replicas":3}}`, "", ""),
	)
	validatePlanCommands(p)
	require.Len(t, p.Actions, 1, "unrunnable execute is dropped")
	assert.Equal(t, "kubectl get deployment web -n demo -o jsonpath='{.spec.replicas}'", p.Actions[0].VerifyCommand, "unrunnable verify is replaced by the derived, quoted read")
	assert.Empty(t, p.Actions[0].RollbackCommand, "unrunnable rollback is cleared")

	assert.NotEmpty(t, remediationCommandRejection("kubectl get pods -n demo -o jsonpath={.items[*].metadata.name}"))
	assert.Empty(t, remediationCommandRejection("kubectl get pods -n demo -o jsonpath='{.items[*].metadata.name}'"))
	assert.Empty(t, remediationCommandRejection(`aws ec2 describe-instances --region us-east-1 --query "Reservations[].Instances[?State.Name=='running']"`))
}
