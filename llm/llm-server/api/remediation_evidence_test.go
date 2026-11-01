package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/google/shlex"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// Audit case 12: a hand-written cart scenario posted on a benchmark-server alert; only client-only blocks survive.
func TestClientOnlySectionsKeepsOnlyThresholdAndAlertRule(t *testing.T) {
	ctx := "Deployment cart in namespace demo is OOMKilled repeatedly.\n\n## Threshold suggestion already computed by triage\nCurrent threshold: 5 -> suggested: 8\n\n## Alert rule this event came from\nRule: KubePodNotReady"
	out := clientOnlySections(ctx)
	require.NotContains(t, out, "cart")
	require.Contains(t, out, "suggested: 8")
	require.Contains(t, out, "Rule: KubePodNotReady")
}

func TestStoredAnalysisEvidenceSendsTheInvestigationOnce(t *testing.T) {
	rec := EventAnalysisResponse{Summary: "S", Investigation: "INV", DetailedResponse: "INV\n## Affected", GitDiff: "-a\n+b", FilesModified: []any{"x.go"}}
	out := storedAnalysisEvidence(rec)
	require.Equal(t, 1, strings.Count(out, "INV"))
	require.Contains(t, out, "## Code fix already identified by code analysis")
	require.Contains(t, out, "Files: x.go")
}

// The code-fix surface is available only when the evidence the server read carries one.
func TestRemediationArtifactsTrustTheServerForCodeFix(t *testing.T) {
	require.Equal(t, []string{"alert_rule"}, remediationArtifacts([]string{"code_fix", "alert_rule"}, false))
	require.Equal(t, []string{"threshold", "code_fix"}, remediationArtifacts([]string{"threshold"}, true))
}

// Only a recorded firing is stated: a same-finding_id re-fire records none, so absence proves nothing.
func TestRenderAttemptStatesOnlyARecordedFiring(t *testing.T) {
	a := attempt{Kind: "CommandExecution", Ref: "kubectl set resources deployment relay-server -n nudgebee --limits=memory=512Mi",
		Status: "Success", At: time.Now(), FiredAfter: 7}
	require.Contains(t, renderAttempt(a), "a firing of this alert was recorded after it (7 recorded)")
	a.FiredAfter = 0
	line := renderAttempt(a)
	require.NotContains(t, line, "not fired")
	require.True(t, strings.HasSuffix(line, "-> success"), "no claim either way when nothing was recorded: %s", line)
}

// The latest firing is a lower bound, and an end is stated only when the source recorded a real one.
func TestLastFiredLineStatesTheLatestRecordedFiring(t *testing.T) {
	now := time.Now().UTC()
	start := now.Add(-2 * time.Hour)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "sqlmock")}

	mock.ExpectQuery(`MAX\(d.created_at\)`).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
	line := lastFiredLine(nil, dbms, "a", "e", &eventFacts{StartsAt: start, EndsAt: now.Add(-time.Hour)}, now)
	require.Contains(t, line, "Latest recorded firing: "+start.Format(time.RFC3339))
	require.Contains(t, line, "The source recorded an end at")
	require.NotContains(t, line, "Recovered")

	// Cloud alarms are stored with end == start at ingest: that is not a recorded end.
	mock.ExpectQuery(`MAX\(d.created_at\)`).WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))
	require.NotContains(t, lastFiredLine(nil, dbms, "a", "e", &eventFacts{StartsAt: start, EndsAt: start}, now), "recorded an end")
	require.NoError(t, mock.ExpectationsWereMet())
}

// Eval case K2: a JSON payload inside a command string came back cut at the first inner quote. As an
// argument list the payload is one argument, quoted by the server.
func TestCommandFromArgvQuotesWhatNeedsIt(t *testing.T) {
	cmd, ok := commandFromArgv([]string{"kubectl", "patch", "configmap", "eval-k2-cm", "-n", "demo", "--type", "merge",
		"-p", `{"data":{"DB_URL":"postgres://db:5432/app"}}`})
	require.True(t, ok)
	require.Equal(t, `kubectl patch configmap eval-k2-cm -n demo --type merge -p '{"data":{"DB_URL":"postgres://db:5432/app"}}'`, cmd)
	require.Empty(t, remediationCommandRejection(cmd), "the built command must pass the executor's own checks")

	cmd, ok = commandFromArgv([]string{"aws", "cloudwatch", "describe-alarms", "--query", "MetricAlarms[0].StateValue", "--region", "us-east-1"})
	require.True(t, ok)
	require.Equal(t, "aws cloudwatch describe-alarms --query 'MetricAlarms[0].StateValue' --region us-east-1", cmd)

	// Eval case G5: a verify whose format expression quotes a key.
	cmd, ok = commandFromArgv([]string{"gcloud", "run", "services", "describe", "svc", "--region=us-central1",
		"--format=value(spec.template.metadata.annotations['autoscaling.knative.dev/maxScale'])"})
	require.True(t, ok)
	require.Equal(t, `gcloud run services describe svc --region=us-central1 "--format=value(spec.template.metadata.annotations['autoscaling.knative.dev/maxScale'])"`, cmd)
	require.Empty(t, remediationCommandRejection(cmd))

	cmd, ok = commandFromArgv([]string{"kubectl", "get", "configmap", "cm", "-n", "demo", "-o", "jsonpath={.data.DB_URL}"})
	require.True(t, ok)
	require.Empty(t, remediationCommandRejection(cmd))

	_, ok = commandFromArgv([]string{"echo", `it's "$HOME"`})
	require.False(t, ok, "neither quoting carries both quote kinds and a $ literally")
}

func TestNormalizeBuildsCommandsFromArgvAndReportsWhatItCannot(t *testing.T) {
	plan := &RemediationPlan{RootCause: "x", Actions: []RemediationAction{
		{Action: "Raise limit", Kind: RemediationKindFix, Confidence: 90,
			ExecuteArgv: []string{"kubectl", "set", "resources", "deployment", "app", "-n", "web", "--limits=memory=1Gi"}},
		{Action: "Bad quoting", Kind: RemediationKindFix, Confidence: 80, ExecuteArgv: []string{"kubectl", "annotate", "deploy", "app", `note=it's "$x"`}},
	}}
	notes := normalizePlan(plan, nil, nil)
	require.Len(t, plan.Actions, 1, "an action whose command cannot be built has no command and no artifact")
	require.Equal(t, "kubectl set resources deployment app -n web --limits=memory=1Gi", plan.Actions[0].ExecuteCommand)
	require.Contains(t, ruleNames(notes), "argv_not_representable")

	// Found in review: an unrepresentable argument list must not erase a command the model also wrote.
	plan = &RemediationPlan{RootCause: "x", Actions: []RemediationAction{{Action: "Restart", Kind: RemediationKindMitigation, Confidence: 30,
		ExecuteCommand: "kubectl rollout restart deployment app -n web", ExecuteArgv: []string{"kubectl", "annotate", "deploy", "app", `note=it's "$x"`}}}}
	normalizePlan(plan, nil, nil)
	require.Len(t, plan.Actions, 1)
	require.Equal(t, "kubectl rollout restart deployment app -n web", plan.Actions[0].ExecuteCommand)
}

// Hardening is carried apart from actions, so a desirable change is never ranked as the incident's fix.
func TestOptionalHardeningIsNotAnAction(t *testing.T) {
	var plan RemediationPlan
	require.NoError(t, json.Unmarshal([]byte(`{"root_cause":"one-off bad input","actions":[],
		"optional_hardening":[{"target":"lambda_function.py","change":"reject malformed batches","reason":"a future bad batch fails cleanly"}]}`), &plan))
	normalizePlan(&plan, nil, nil)
	require.Empty(t, plan.Actions)
	require.Len(t, plan.OptionalHardening, 1)
	require.Equal(t, "lambda_function.py", plan.OptionalHardening[0].Target)
}

// The shape stored for relay-server in the dev inventory.
func TestManagedByReadsHelmAndArgoMarkers(t *testing.T) {
	helm := map[string]any{"is_helm_release": true, "config": map[string]any{
		"labels":      map[string]any{"app.kubernetes.io/managed-by": "Helm"},
		"annotations": map[string]any{"meta.helm.sh/release-name": "relay-server", "ci.nudgebee.com/helm.values.filePath": "deploy/kubernetes/relay-server/values-dev.yaml"},
	}}
	require.Equal(t, "Helm release relay-server (values file deploy/kubernetes/relay-server/values-dev.yaml)", managedBy(helm))
	require.Equal(t, "Argo CD application shop", managedBy(map[string]any{"config": map[string]any{"labels": map[string]any{"argocd.argoproj.io/instance": "shop"}}}))
	require.Equal(t, "", managedBy(map[string]any{"config": map[string]any{"labels": map[string]any{"app": "x"}}}))
}

func TestConclusionSectionStatesTheInvestigationsChangePlan(t *testing.T) {
	out := conclusionSection(&codeChangePlan{Kind: "source", Files: []string{"handlers/prometheus.go"}, Change: "stream large replies"})
	require.Contains(t, out, "## Investigation conclusion")
	require.Contains(t, out, "Where the root cause is: application source code.")
	require.Contains(t, out, "Files: handlers/prometheus.go")
	require.Contains(t, out, "Required change: stream large replies")
	require.Equal(t, "", conclusionSection(nil))
}

// A fix that lives in code or chart values is still the fix. It used to be dropped for having no
// command, which left only the command-shaped stopgaps.
func TestNormalizeKeepsAFixStatedAsARequiredChange(t *testing.T) {
	plan := &RemediationPlan{RootCause: "x", Actions: []RemediationAction{
		{Action: "Raise limit in chart", Kind: RemediationKindFix, Confidence: 85,
			RequiredChange: &RequiredChange{Where: "deployment_source", Target: "deploy/kubernetes/relay-server/values-dev.yaml resources.limits.memory", Change: "307Mi -> 1Gi"},
			VerifyCommand:  "kubectl get deploy relay-server"},
		{Action: "Tune it", Kind: RemediationKindFix, Confidence: 80, RequiredChange: &RequiredChange{Where: "code", Change: "improve memory handling"}},
	}}
	notes := normalizePlan(plan, nil, nil)
	require.Len(t, plan.Actions, 1, "a required change with no target is not actionable")
	require.Equal(t, "Raise limit in chart", plan.Actions[0].Action)
	require.Equal(t, RemediationKindFix, plan.Actions[0].Kind)
	require.Empty(t, plan.Actions[0].VerifyCommand, "nothing ran, so nothing to verify")
	require.Contains(t, ruleNames(notes), "no_command_and_no_artifact_dropped")
}

// Execution is bound to the stored plan: generation is grounded server-side, so a command string
// from the caller must be one the plan holds, in the slot it is run as.
func TestPlanHasCommandBindsExecutionToThePlan(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{{
		ExecuteCommand:  "kubectl set resources deployment app -n web --limits=memory=1Gi",
		VerifyCommand:   "kubectl get deployment app -n web -o jsonpath='{.spec.template.spec.containers[*].resources}'",
		RollbackCommand: "kubectl set resources deployment app -n web --limits=memory=512Mi",
	}}}
	a := plan.Actions[0]
	require.True(t, planHasCommand(plan, "execute", a.ExecuteCommand, ""))
	require.True(t, planHasCommand(plan, "", " "+a.ExecuteCommand+" ", ""), "empty slot is execute")
	require.True(t, planHasCommand(plan, "verify", a.VerifyCommand, a.ExecuteCommand))
	require.True(t, planHasCommand(plan, "rollback", a.RollbackCommand, ""))

	require.False(t, planHasCommand(plan, "execute", "kubectl delete deployment app -n web", ""), "not in the plan")
	require.False(t, planHasCommand(plan, "execute", a.VerifyCommand, ""), "a verify is not an execute")
	require.False(t, planHasCommand(plan, "verify", a.VerifyCommand, "kubectl scale deployment other --replicas=3"), "verify of another action")
	require.False(t, planHasCommand(plan, "rollback", a.ExecuteCommand, ""))
}

// Review: a double-quoted argument holding one apostrophe was rejected as truncated, and the action
// dropped, because each quote character was counted on its own.
func TestSingleApostropheArgumentIsNotTruncation(t *testing.T) {
	for _, argv := range [][]string{
		{"aws", "sns", "publish", "--topic-arn", "arn:aws:sns:us-east-1:123456789012:ops", "--message", "it's done", "--region", "us-east-1"},
		{"kubectl", "annotate", "deployment", "app", "-n", "web", "note=it's fixed", "--overwrite"},
	} {
		cmd, ok := commandFromArgv(argv)
		require.True(t, ok)
		require.False(t, isStructurallyTruncated(cmd), cmd)
	}
	cmd, _ := commandFromArgv([]string{"aws", "sns", "publish", "--message", "it's done", "--region", "us-east-1"})
	require.Empty(t, remediationCommandRejection(cmd), cmd)
	// A command cut at an inner quote is still caught.
	require.True(t, isStructurallyTruncated(`kubectl patch configmap flagd-config -n demo -p '{`))
	require.True(t, isStructurallyTruncated(`aws sns publish --message "it's`))
	// Outside quotes a backslash escapes the quote; inside single quotes it is literal.
	require.False(t, isStructurallyTruncated(`echo it\'s done`))
	require.False(t, isStructurallyTruncated(`echo 'a\' b`))
}

// Review: only the investigation is required. 44 of 2,110 dev events with a finished investigation
// had another stage not COMPLETED and got no plan.
func TestLoadRemediationAnalysisNeedsOnlyTheInvestigation(t *testing.T) {
	rows := map[events.EventAnalysisType]*events.EventAnalysis{
		events.AnalysisTypeInvestigation: {Status: "COMPLETED", Summary: "INV"},
	}
	completed := func(t events.EventAnalysisType) *events.EventAnalysis { return rows[t] }
	rec, err := loadRemediationAnalysis(completed)
	require.NoError(t, err)
	require.Equal(t, "INV", rec.Investigation)
	require.Empty(t, rec.DetailedResponse)

	rows[events.AnalysisTypeSummary] = &events.EventAnalysis{Status: "COMPLETED", Summary: "S"}
	rows[events.AnalysisTypeDetailedResponse] = &events.EventAnalysis{Status: "COMPLETED", Summary: "REPORT"}
	rows[events.AnalysisTypeLog] = &events.EventAnalysis{Status: "COMPLETED", Summary: "L", Analysis: `{"git_diff":"-a\n+b","files_modified":["x.go"]}`}
	rec, err = loadRemediationAnalysis(completed)
	require.NoError(t, err)
	require.Equal(t, "S", rec.Summary, "the summary stage wins over the log row's")
	require.Equal(t, "REPORT", rec.DetailedResponse)
	require.Contains(t, codeFixEvidence(rec), "Proposed diff:\n-a\n+b")

	delete(rows, events.AnalysisTypeInvestigation)
	_, err = loadRemediationAnalysis(completed)
	require.Error(t, err, "no investigation, no plan")
}

// Review: the stored plan is Run's allow-list; a missing plan or an event-less request refuses.
func TestPlanCommandRejectionFor(t *testing.T) {
	plan := RemediationPlan{Actions: []RemediationAction{{ExecuteCommand: "kubectl scale deployment app -n web --replicas 3",
		VerifyCommand: "kubectl get deployment app -n web -o jsonpath={.spec.replicas}"}}}
	found := func() (RemediationPlan, bool) { return plan, true }
	missing := func() (RemediationPlan, bool) { return RemediationPlan{}, false }
	req := RemediationExecuteRequest{EventId: "e", Slot: "execute", Command: plan.Actions[0].ExecuteCommand}

	require.Empty(t, planCommandRejectionFor(req, found))
	require.Contains(t, planCommandRejectionFor(req, missing), "no remediation plan")
	require.Contains(t, planCommandRejectionFor(RemediationExecuteRequest{Command: req.Command}, found), "only as part of an event's plan")
	req.Command = "kubectl delete deployment app -n web"
	require.Contains(t, planCommandRejectionFor(req, found), "not part of the event's remediation plan")

	verify := RemediationExecuteRequest{EventId: "e", Slot: "verify", Command: plan.Actions[0].VerifyCommand}
	require.Contains(t, planCommandRejectionFor(verify, found), "not part", "a verify must name the execute it checks")
	verify.ExecuteCommand = plan.Actions[0].ExecuteCommand
	require.Empty(t, planCommandRejectionFor(verify, found))
}

func TestLoadChangePlanReadsTheInvestigationRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "sqlmock")}
	mock.ExpectQuery(`FROM event_log_analysis a\s+JOIN events e ON e.id = a.event_id AND e.cloud_account_id = \$2\s+WHERE a.event_id = \$1 AND a.analysis_type = 'investigation'`).
		WithArgs("evt", "acc").
		WillReturnRows(sqlmock.NewRows([]string{"analysis"}).AddRow(`{"kind":"source","files":["h.go"],"change":"stream replies"}`))
	plan := loadChangePlan(nil, dbms, "acc", "evt")
	require.NotNil(t, plan)
	require.Equal(t, "source", plan.Kind)

	mock.ExpectQuery(`FROM event_log_analysis`).WillReturnRows(sqlmock.NewRows([]string{"analysis"}).AddRow(`{"confirming_check":{}}`))
	require.Nil(t, loadChangePlan(nil, dbms, "acc", "evt"), "an analysis that is not a change plan")
	require.NoError(t, mock.ExpectationsWereMet())
}

// LATERAL join before WHERE (after it was a swallowed syntax error), bounded by the facts' moment;
// other-namespace attempts are left out.
func TestAttemptsSectionReportsRecordedFiringsAfterEachAttempt(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "sqlmock")}
	now := time.Now().UTC()
	mock.ExpectQuery(`FROM event_resolution er\s+JOIN events other .*\s+JOIN events e .*\s+CROSS JOIN LATERAL \(.*\) fired\s+WHERE er.type IN`).
		WithArgs("evt", "acc", 4*maxAttempts, now).
		WillReturnRows(sqlmock.NewRows([]string{"type", "ref", "status", "lifecycle", "branch", "data", "created_at", "same_event", "n", "last"}).
			AddRow("CommandExecution", "kubectl set resources deployment app -n web --limits=memory=512Mi", "Success", "", "", []byte(`{}`), now.Add(-time.Hour), true, 3, now.Add(-10*time.Minute)).
			AddRow("CommandExecution", "kubectl set resources deployment cart -n demo --limits=memory=1Gi", "Success", "", "", []byte(`{}`), now.Add(-2*time.Hour), false, 0, nil))
	out := attemptsSection(security.NewRequestContextForSuperAdmin(), dbms, "acc", "evt", "web", now)
	require.Contains(t, out, "a firing of this alert was recorded after it (3 recorded)")
	require.NotContains(t, out, "cart", "an attempt in another namespace is out of scope")
	require.NoError(t, mock.ExpectationsWereMet())
}

// argv is serialised for the relay, which runs the command as POSIX shell text: every representable
// argument must come back from a shell split exactly as the model wrote it.
func TestCommandFromArgvRoundTripsThroughTheShell(t *testing.T) {
	for name, argv := range map[string][]string{
		"spaces":        {"kubectl", "annotate", "deployment", "api", "-n", "shop", "note=two words", "--overwrite"},
		"single quote":  {"kubectl", "annotate", "deployment", "api", "-n", "shop", "note=it's fine", "--overwrite"},
		"double quotes": {"kubectl", "annotate", "deployment", "api", "-n", "shop", `note=say "hi"`, "--overwrite"},
		"json payload": {"kubectl", "patch", "deployment", "api", "-n", "shop", "--type=merge", "-p",
			`{"spec":{"template":{"spec":{"containers":[{"name":"api","resources":{"limits":{"memory":"512Mi"}}}]}}}}`},
		"jsonpath":       {"kubectl", "get", "deployment", "api", "-n", "shop", "-o", "jsonpath={.spec.template.spec.containers[*].resources}"},
		"jsonpath range": {"kubectl", "get", "pods", "-n", "shop", "-o", `jsonpath={range .items[*]}{.metadata.name}{"\n"}{end}`},
		"aws filter":     {"aws", "ec2", "describe-instances", "--filters", "Name=tag:Name,Values=web server", "--region", "us-east-1"},
		"aws jmespath":   {"aws", "ec2", "describe-instances", "--query", "Reservations[].Instances[?State.Name=='running'].InstanceId", "--region", "us-east-1"},
	} {
		cmd, ok := commandFromArgv(argv)
		require.True(t, ok, name)
		split, err := shlex.Split(cmd)
		require.NoError(t, err, name)
		require.Equal(t, argv, split, "%s: %s", name, cmd)
		require.Empty(t, remediationCommandRejection(cmd), "%s: %s", name, cmd)
	}
}

// Quoting keeps an argument literal, but generic safety still refuses shell syntax anywhere in a
// kubectl command: the serialiser must not become a way around it.
func TestArgvShellMetacharactersStayRejected(t *testing.T) {
	for _, arg := range []string{"pods;", "$(whoami)", "`id`", "a|b", "a && b", "a > /etc/passwd", "a\nkubectl delete ns prod", "$HOME"} {
		cmd, ok := commandFromArgv([]string{"kubectl", "get", "pods", "-n", "shop", arg})
		require.True(t, ok, arg)
		require.NotEmpty(t, remediationCommandRejection(cmd), "%q -> %s", arg, cmd)
	}
	// Neither quoting carries both a ' and a shell-active character literally: refuse, don't guess.
	for _, arg := range []string{`it's "$HOME"`, "it's `id`", `it's \ back`} {
		_, ok := commandFromArgv([]string{"kubectl", "annotate", "deployment", "api", "note=" + arg})
		require.False(t, ok, arg)
	}
}

// useMetastore points common.Metastore at a sqlmock for one test.
func useMetastore(t *testing.T) sqlmock.Sqlmock {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	common.RegisterDatabaseManagerHook(common.Metastore, func() (*common.DatabaseManager, error) {
		return &common.DatabaseManager{Db: sqlx.NewDb(db, "sqlmock")}, nil
	})
	t.Cleanup(func() {
		common.RegisterDatabaseManagerHook(common.Metastore, nil)
		_ = db.Close()
	})
	mock.MatchExpectationsInOrder(false)
	return mock
}

// expectStage answers one analysis stage read (no mapping, then the fingerprint lookup); status "" means no row.
func expectStage(mock sqlmock.Sqlmock, stage events.EventAnalysisType, status, summary string) {
	mock.ExpectQuery(`FROM event_analysis_mapping`).WithArgs("evt", stage).WillReturnError(sql.ErrNoRows)
	rows := sqlmock.NewRows([]string{"id", "analysis", "status", "event_id", "summary", "status_reason", "updated_at"})
	if status != "" {
		rows.AddRow("row-"+string(stage), "{}", status, "evt", summary, "", time.Now())
	}
	mock.ExpectQuery(`FROM event_log_analysis WHERE event_fingerprint`).WithArgs("fp", "acc", "agg", stage).WillReturnRows(rows)
}

func expectEvent(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`FROM events WHERE id = \$1`).WithArgs("evt", "acc").
		WillReturnRows(sqlmock.NewRows([]string{"id", "fingerprint", "aggregation_key", "created_at"}).AddRow("evt", "fp", "agg", time.Now()))
}

// A completed investigation is enough; a log or summary stage still running or failed must not block the plan.
func TestRemediationEvidencePlansFromACompletedInvestigationAlone(t *testing.T) {
	mock := useMetastore(t)
	expectEvent(mock)
	expectStage(mock, events.AnalysisTypeInvestigation, "COMPLETED", "INV: the pod is OOMKilled at 256Mi")
	expectStage(mock, events.AnalysisTypeLog, "IN_PROGRESS", "half a log analysis")
	expectStage(mock, events.AnalysisTypeSummary, "FAILED", "a failed summary")
	expectStage(mock, events.AnalysisTypeDetailedResponse, "", "")

	evidence, _, err := remediationEvidence(security.NewRequestContextForSuperAdmin(), "acc", "evt", "")
	require.NoError(t, err)
	require.Contains(t, evidence, "INV: the pod is OOMKilled")
	require.NotContains(t, evidence, "half a log analysis")
	require.NotContains(t, evidence, "a failed summary")
	require.NoError(t, mock.ExpectationsWereMet())
}

// An investigation that has not completed is refused as input, before the model is asked anything,
// even when the other stages are done.
func TestGenerateRefusesAnIncompleteInvestigation(t *testing.T) {
	for _, status := range []string{"IN_PROGRESS", "FAILED", ""} {
		mock := useMetastore(t)
		expectEvent(mock)
		expectStage(mock, events.AnalysisTypeInvestigation, status, "partial")
		expectStage(mock, events.AnalysisTypeLog, "COMPLETED", "log")
		expectStage(mock, events.AnalysisTypeSummary, "COMPLETED", "summary")
		expectStage(mock, events.AnalysisTypeDetailedResponse, "COMPLETED", "report")

		_, _, err := generateRemediationPlan(security.NewRequestContextForSuperAdmin(),
			RemediationGenerateRequest{AccountId: "acc", EventId: "evt", Context: "## Investigation\nclient-supplied text"},
			func(string, string) (string, error) {
				t.Fatalf("the model was called for investigation status %q", status)
				return "", nil
			})
		var input errRemediationInput
		require.ErrorAs(t, err, &input, status)
	}
}

// A plan that is not stored cannot be run (Run checks the stored plan), so it must not be returned.
func TestPersistRemediationPlanReportsEveryFailure(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	require.Error(t, persistRemediationPlan(ctx, "acc", "", RemediationPlan{}, RemediationTrace{}), "no event to store it under")

	mock := useMetastore(t)
	mock.ExpectQuery(`FROM events WHERE id = \$1`).WithArgs("evt", "acc").WillReturnError(errors.New("connection reset"))
	require.ErrorContains(t, persistRemediationPlan(ctx, "acc", "evt", RemediationPlan{}, RemediationTrace{}), "connection reset")

	expectEvent(mock)
	mock.ExpectBegin().WillReturnError(errors.New("too many connections"))
	require.ErrorContains(t, persistRemediationPlan(ctx, "acc", "evt", RemediationPlan{}, RemediationTrace{}), "too many connections")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGenerateFailsWhenThePlanCannotBeSaved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	plan := RemediationPlan{Summary: "raise the limit", Actions: []RemediationAction{{ExecuteCommand: "kubectl set resources deployment api -n shop --limits=memory=512Mi"}}}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	respondWithStoredPlan(c, slog.Default(), plan, func() error { return errors.New("event info unavailable: connection reset") })
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Contains(t, rec.Body.String(), "failed to save remediation plan")
	require.NotContains(t, rec.Body.String(), "kubectl", "an unsaved plan's commands are not shown")

	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	respondWithStoredPlan(c, slog.Default(), plan, func() error { return nil })
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "kubectl set resources")
}
