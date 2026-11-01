package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"

	"nudgebee/llm/common"
)

// meta mirrors how a stored config blob actually arrives: decoded from JSON, so every number is a
// float64 and nested values are maps rather than Go structs.
func meta(t *testing.T, raw string) map[string]any {
	t.Helper()
	out := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(raw), &out))
	return out
}

func TestLinkedArnsSkipsTheResourcesOwnIdentifier(t *testing.T) {
	self := "arn:aws:sqs:us-east-1:123456789012:orders"
	m := meta(t, `{
		"QueueArn": "arn:aws:sqs:us-east-1:123456789012:orders",
		"RedrivePolicy": "{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:123456789012:orders-dlq\",\"maxReceiveCount\":5}"
	}`)
	require.Equal(t, []string{"arn:aws:sqs:us-east-1:123456789012:orders-dlq"}, linkedArns(m, self, 2))
}

func TestLinkedArnsIsBoundedAndDeterministic(t *testing.T) {
	m := meta(t, `{
		"a": "arn:aws:sqs:us-east-1:123456789012:c-queue",
		"b": "arn:aws:sqs:us-east-1:123456789012:a-queue",
		"c": "arn:aws:sqs:us-east-1:123456789012:b-queue"
	}`)
	first := linkedArns(m, "", 2)
	require.Len(t, first, 2)
	require.Equal(t, first, linkedArns(m, "", 2), "map iteration order must not leak into the prompt")
}

// A zero is the whole story in a throttled-consumer incident, so it must survive rendering.
func TestRenderResourceKeepsAZeroValuedSetting(t *testing.T) {
	res := &inventoryResource{Name: "invoice-processor", Type: "function", LastSeen: time.Now(),
		Meta: meta(t, `{"Concurrency": 0, "Timeout": 30, "Runtime": "python3.12"}`)}
	out := renderResource(res)
	require.Contains(t, out, "Concurrency=0")
	require.Contains(t, out, "Runtime=python3.12")
}

func TestRenderResourceDropsLongAndStructuredValues(t *testing.T) {
	res := &inventoryResource{Name: "orders", Type: "queue", LastSeen: time.Now(),
		Meta: meta(t, `{
			"Policy": "`+strings.Repeat("x", maxConfigValue+1)+`",
			"Tags": {"team": "billing"},
			"VisibilityTimeout": 180
		}`)}
	out := renderResource(res)
	require.Contains(t, out, "VisibilityTimeout=180")
	require.NotContains(t, out, "Policy=")
	require.NotContains(t, out, "Tags=")
}

func TestRenderResourceCapsFieldsAndSaysHowManyItDropped(t *testing.T) {
	fields := map[string]any{}
	for i := 0; i < maxConfigFields+4; i++ {
		fields[string(rune('a'+i))+"key"] = "v"
	}
	raw, err := json.Marshal(fields)
	require.NoError(t, err)
	res := &inventoryResource{Name: "q", Type: "queue", LastSeen: time.Now(), Meta: meta(t, string(raw))}
	out := renderResource(res)
	require.Equal(t, maxConfigFields, strings.Count(out, "key=v"))
	require.Contains(t, out, "(+4 more fields)")
}

func TestVerifyOutcomeDistinguishesFailedFromInconclusive(t *testing.T) {
	require.Equal(t, "verification passed", verifyOutcome([]byte(`{"verify":{"ran":true,"passed":true}}`)))
	require.Equal(t, "verification inconclusive", verifyOutcome([]byte(`{"verify":{"ran":true,"passed":null}}`)))
	require.Contains(t, verifyOutcome([]byte(`{"verify":{"ran":true,"passed":false,"output":"still 0"}}`)), "FAILED: still 0")
	require.Empty(t, verifyOutcome([]byte(`{"command":"kubectl get pods"}`)), "an attempt with no verify says nothing about verification")
}

func TestDecodeLabelsRendersNumbersAndSkipsStructures(t *testing.T) {
	got := decodeLabels([]byte(`{"aws_event_threshold": 300, "aws_event_state": "ALARM", "nested": {"a": 1}}`))
	require.Equal(t, "300", got["aws_event_threshold"])
	require.Equal(t, "ALARM", got["aws_event_state"])
	require.NotContains(t, got, "nested")
}

func TestDecodeLabelsSurvivesMalformedJson(t *testing.T) {
	require.Nil(t, decodeLabels([]byte(`not json`)))
}

func TestHumanDurationReadsAsAnIncidentAge(t *testing.T) {
	require.Equal(t, "under a minute", humanDuration(30*time.Second))
	require.Equal(t, "42m", humanDuration(42*time.Minute))
	require.Equal(t, "25h3m", humanDuration(25*time.Hour+3*time.Minute))
}

func TestNonEmptyDropsBlankSections(t *testing.T) {
	require.Equal(t, []string{"## Incident", "## Target"}, nonEmpty([]string{"## Incident", "", "   ", "## Target"}))
}

func TestTruncateMarksWhatItCut(t *testing.T) {
	require.Equal(t, "abc", truncate("abc", 3))
	require.Equal(t, "ab...", truncate("abcd", 2))
}

func TestRenderAttemptNamesAnOpenFixPullRequestAndWhatItChanged(t *testing.T) {
	line := renderAttempt(attempt{Kind: "PullRequest", Ref: "https://github.com/acme/app/pull/38076",
		Lifecycle: "created", Branch: "fix/add-log-level-graphql-gateway-timing", At: time.Now()})
	require.Contains(t, line, "on an earlier firing of this alert: fix pull request https://github.com/acme/app/pull/38076")
	require.Contains(t, line, "(branch fix/add-log-level-graphql-gateway-timing)")
	require.True(t, strings.HasSuffix(line, "-> open"), line)
}

func TestRenderAttemptReportsAMergedFix(t *testing.T) {
	line := renderAttempt(attempt{Kind: "PullRequest", Ref: "https://github.com/acme/app/pull/37143",
		Status: "Success", Lifecycle: "merged", SameEvent: true, At: time.Now()})
	require.Contains(t, line, "on this incident")
	require.Contains(t, line, "-> merged (whether the merge is deployed is not recorded)")
}

func TestRenderAttemptCarriesAFailedVerification(t *testing.T) {
	line := renderAttempt(attempt{Kind: "CommandExecution", Ref: "kubectl rollout restart deployment/app -n web",
		Status: "Success", Data: []byte(`{"verify":{"ran":true,"passed":false,"output":"0/3 ready"}}`), At: time.Now()})
	require.Contains(t, line, "ran kubectl rollout restart deployment/app -n web -> success, verification FAILED: 0/3 ready")
}

// Rows from the dev audit: kubectl runs in demo recorded against a nudgebee/benchmark-server alert.
func TestAttemptInScopeDropsKubectlRunInAnotherNamespace(t *testing.T) {
	cmd := func(ref string) attempt { return attempt{Kind: "CommandExecution", Ref: ref} }
	require.False(t, attemptInScope(cmd(`kubectl patch deployment cart -n demo -p '{}'`), "nudgebee"))
	require.False(t, attemptInScope(cmd("kubectl get pods --namespace=demo"), "nudgebee"))
	require.True(t, attemptInScope(cmd("kubectl set resources deployment benchmark-server -n nudgebee --limits=memory=4Gi"), "nudgebee"))
	require.True(t, attemptInScope(cmd("kubectl get nodes"), "nudgebee"), "no namespace: cannot tell, keep it")
	require.True(t, attemptInScope(cmd("aws ssm send-command --instance-ids i-0150dbd583caa0e69 --region us-east-1"), "AmazonEC2"),
		"a cloud command may legitimately act on a dependency")
	require.True(t, attemptInScope(cmd("kubectl delete pod x -n demo"), ""), "event without a namespace keeps everything")
	require.True(t, attemptInScope(attempt{Kind: "PullRequest", Ref: "https://github.com/acme/app/pull/1"}, "nudgebee"))
}

// A Kubernetes event's subject_node is a node name, and k8s inventory rows carry region 'global',
// A region the event does not carry is not the same as a region that does not apply. #38535
func TestRegionScopeSeparatesNotApplicableFromMissing(t *testing.T) {
	// On k8s subject_node is a node name, and every row is stored 'global': no region to match.
	region, required := regionScope("K8s", "gke-example-pool-1a2b3c4d-x7k9")
	require.Equal(t, "", region)
	require.False(t, required)

	// A cloud resource is identified by its region, so a missing one must not widen the search.
	region, required = regionScope("Azure", "")
	require.Equal(t, "", region)
	require.True(t, required)

	region, required = regionScope("AWS", "us-east-1")
	require.Equal(t, "us-east-1", region)
	require.True(t, required)
}

// Both lookups must be scoped to the account, and the resource lookup must be able to tell apart
// two resources that share a name. Asserted on the SQL because that is where both defects were.
func TestFactLookupsAreScopedToTheAccountAndSubjectType(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "sqlmock")}

	// Fingerprints are shared across accounts, so the duplicate row itself must be account-scoped.
	mock.ExpectQuery(`FROM event_duplicates d.+d\.cloud_account_id = e\.cloud_account_id`).
		WithArgs("event-1", "account-1", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"occurrence_number", "absolute_first_seen_at"}).
			AddRow(4, time.Now().Add(-2*time.Hour)))
	require.Contains(t, occurrenceLine(nil, dbms, "account-1", "event-1", time.Now()), "at least 4 recorded firings")

	// The subject's type ranks a name collision; it is bound, never interpolated.
	mock.ExpectQuery(`FROM cloud_resourses.+ORDER BY \(lower\(COALESCE\(type,''\)\) = lower\(\$4\)\) DESC`).
		WithArgs("account-1", "analytics-exporter", "", "deployment", "").
		WillReturnRows(sqlmock.NewRows([]string{"name", "type", "arn", "last_seen", "meta"}).
			AddRow("analytics-exporter", "Deployment", "", time.Now(), []byte(`{"replicas":1}`)))
	res := loadResource(dbms, "account-1", "analytics-exporter", "", "deployment", "")
	require.NotNil(t, res)
	require.Equal(t, "Deployment", res.Type)

	require.NoError(t, mock.ExpectationsWereMet())
}

// events.starts_at is nullable, and a plain time.Time rejected the whole row -> every fact for that
// incident was dropped, not just its age. Seen on a real dev event. #38594
func TestLoadEventFactsSurvivesANullStartTime(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery(`FROM events WHERE id`).WithArgs("evt-1", "acc-1").
		WillReturnRows(sqlmock.NewRows([]string{"title", "aggregation_key", "subject_type", "subject_name",
			"subject_namespace", "subject_node", "fingerprint", "starts_at", "ends_at", "labels", "subject_owner", "subject_owner_kind"}).
			AddRow("OOMKilled: cart", "OOMKilled: cart", "pod", "cart-6d4f", "demo", "", "fp-1", nil, nil, []byte(`{}`), "cart", "Deployment"))

	facts := loadEventFacts(nil, &common.DatabaseManager{Db: sqlx.NewDb(db, "sqlmock")}, "acc-1", "evt-1")

	require.NotNil(t, facts, "a null start time must not cost us the rest of the incident")
	require.Equal(t, "cart-6d4f", facts.SubjectName)
	require.Equal(t, "cart", facts.Owner, "a pod's owner is what a command changes")
	require.True(t, facts.StartsAt.IsZero(), "an unknown start time is reported as unknown, not invented")
}

// cloud_resourses.last_seen is nullable too, and printing the zero value would date the row to year
// 1 — a fabricated fact, where saying nothing is honest.
func TestRenderResourceOmitsAnUnknownLastSeen(t *testing.T) {
	rendered := renderResource(&inventoryResource{
		Name: "cart", Type: "Deployment",
		Meta: meta(t, `{"namespace":"demo"}`),
	})

	require.Equal(t, "Deployment cart: namespace=demo", rendered)
	require.NotContains(t, rendered, "0001-01-01")
}

// The snapshot has to carry what a command acts on — the container to address, the values a fix
// changes, and the state the workload is in — not just the fields one ticket happened to name.
func TestWorkloadConfigLinesDescribeTheWorkload(t *testing.T) {
	lines := workloadConfigLines(meta(t, `{
		"status_info": {
			"replicas": 3,
			"conditions": [
				{"type": "Available", "status": "False", "reason": "MinimumReplicasUnavailable"},
				{"type": "Progressing", "status": "True", "reason": "ReplicaSetUpdated"}
			]
		},
		"config": {
			"annotations": {"deployment.kubernetes.io/revision": "4"},
			"containers": [{
				"name": "cart",
				"image": "ghcr.io/nudgebee/cart:1.2",
				"resources": {"requests": {"cpu": "100m", "memory": "100Mi"}, "limits": {"memory": "160Mi"}},
				"readiness_probe": {"httpGet": {"path": "/health"}},
				"env": [{"name": "GOMEMLIMIT", "value": "120MiB"}, {"name": "DBADDR", "value": "redis:6379"}]
			}]
		}
	}`))

	require.Equal(t, []string{
		"  container cart: image=ghcr.io/nudgebee/cart:1.2 requests=cpu:100m,memory:100Mi limits=memory:160Mi probes=readiness env=GOMEMLIMIT,DBADDR",
		"  rollout revision: 4",
		"  desired replicas: 3",
		"  conditions: Available=False (MinimumReplicasUnavailable), Progressing=True (ReplicaSetUpdated)",
	}, lines)
}

// An env value can be a credential; the name alone is what tells the model the setting exists.
func TestWorkloadConfigLinesNeverStatesEnvValues(t *testing.T) {
	lines := workloadConfigLines(meta(t, `{"config":{"containers":[{"name":"api","env":[
		{"name":"DB_PASSWORD","value":"hunter2"},{"name":"TOKEN","valueFrom":{"secretKeyRef":{"name":"s"}}}]}]}}`))

	require.Len(t, lines, 1)
	require.Contains(t, lines[0], "env=DB_PASSWORD,TOKEN")
	require.NotContains(t, lines[0], "hunter2")
}

// A cloud resource has no workload blob, and a workload may be collected without one.
func TestWorkloadConfigLinesSkipsWhatItCannotRead(t *testing.T) {
	require.Nil(t, workloadConfigLines(meta(t, `{"node": "gke-node-1"}`)))
	require.Nil(t, workloadConfigLines(meta(t, `{"config": {"labels": {"app": "cart"}}}`)))
	require.Equal(t, []string{"  rollout revision: 4"},
		workloadConfigLines(meta(t, `{"config":{"containers":[{"image":"x:1"}],"annotations":{"deployment.kubernetes.io/revision":"4"}}}`)),
		"a container with no name cannot be addressed, so it is not offered")
}

// Every container is addressable, so a sidecar must be listed too — bounded, since a pod spec and
// its env list can both be long.
func TestWorkloadConfigLinesAreBounded(t *testing.T) {
	containers := make([]string, 0, maxContainers+2)
	for i := 0; i < maxContainers+2; i++ {
		containers = append(containers, fmt.Sprintf(`{"name":"c%d","image":"img:%d"}`, i, i))
	}
	lines := workloadConfigLines(meta(t, `{"config": {"containers": [`+strings.Join(containers, ",")+`]}}`))
	require.Len(t, lines, maxContainers)

	env := make([]string, 0, maxEnvNames+3)
	for i := 0; i < maxEnvNames+3; i++ {
		env = append(env, fmt.Sprintf(`{"name":"VAR%d"}`, i))
	}
	envLines := workloadConfigLines(meta(t, `{"config":{"containers":[{"name":"api","env":[`+strings.Join(env, ",")+`]}]}}`))
	require.Len(t, envLines, 1)
	require.Contains(t, envLines[0], fmt.Sprintf("VAR%d", maxEnvNames-1))
	require.NotContains(t, envLines[0], fmt.Sprintf("VAR%d,", maxEnvNames))
}

// The workload facts must reach the block that is actually sent to the model.
func TestRenderResourceCarriesTheWorkloadConfig(t *testing.T) {
	rendered := renderResource(&inventoryResource{
		Name: "hello-world", Type: "Deployment", LastSeen: time.Now(),
		Meta: meta(t, `{"namespace":"default","ready_pods":0,"total_pods":1,
			"config":{"containers":[{"name":"nginx","image":"nginx:latest-01"}],
			"annotations":{"deployment.kubernetes.io/revision":"1"}}}`),
	})

	require.Contains(t, rendered, "Deployment hello-world")
	require.Contains(t, rendered, "ready_pods=0")
	require.Contains(t, rendered, "container nginx: image=nginx:latest-01")
	require.Contains(t, rendered, "rollout revision: 1")
}

// A readiness reading is only as good as its age: "Ready a week ago" and "Ready a minute ago"
// support opposite decisions about whether to act.
func TestRenderResourceStatesHowOldTheReadingIs(t *testing.T) {
	rendered := renderResource(&inventoryResource{
		Name: "cart", Type: "Deployment", LastSeen: time.Now().Add(-90 * time.Minute),
		Meta: meta(t, `{"ready_pods":1,"total_pods":1}`),
	})

	require.Contains(t, rendered, "ago)")
	require.Contains(t, rendered, "ready_pods=1")
}

// Over budget, the configuration gives way — not what was already attempted, which is the section
// that stops a failed command being proposed a second time.
func TestWithinBudgetKeepsWhatWasAlreadyAttempted(t *testing.T) {
	attempts := "## Remediation already attempted\n- 2026-09-20T06:12:53Z on this incident: ran kubectl scale … -> failed"
	sections := []string{
		"## Incident\nAlert: OOMKilled",
		configSectionHeading + "\n" + strings.Repeat("  container c: image=x resources=y\n", 200),
		attempts,
	}

	out := withinBudget(sections)

	require.LessOrEqual(t, len(out), factsBudget+len("\n  [configuration truncated]"))
	require.Contains(t, out, attempts, "the attempts section must survive")
	require.Contains(t, out, "## Incident")
	require.Contains(t, out, "[configuration truncated]")
}

// The common case must pass through untouched, with no truncation marker to explain away.
func TestWithinBudgetLeavesASmallBlockAlone(t *testing.T) {
	sections := []string{"## Incident\nAlert: OOMKilled", configSectionHeading + "\n  container cart: image=x"}

	out := withinBudget(sections)

	require.Equal(t, strings.Join(sections, "\n\n"), out)
	require.NotContains(t, out, "truncated")
}

// A workload name repeats across namespaces — dev holds 17 Deployments called llm-server — so the
// event's namespace decides which one, or the facts describe someone else's workload.
func TestLoadResourcePrefersTheEventsNamespace(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "sqlmock")}

	mock.ExpectQuery(`FROM cloud_resourses`).
		WithArgs("acc-1", "llm-server", "", "deployment", "nudgebee").
		WillReturnRows(sqlmock.NewRows([]string{"name", "type", "arn", "last_seen", "meta"}).
			AddRow("llm-server", "Deployment", "", time.Now(), []byte(`{"namespace":"nudgebee","ready_pods":2}`)))

	res := loadResource(dbms, "acc-1", "llm-server", "", "deployment", "nudgebee")

	require.NotNil(t, res)
	require.Equal(t, "nudgebee", res.Meta["namespace"])
	require.NoError(t, mock.ExpectationsWereMet())
}

// These values become remediation facts, so a whole number must read as one: the trailing-zero trim
// stops at the decimal point, which is what keeps 100.0000 from becoming 1.
func TestScalarStringFormatsNumbers(t *testing.T) {
	for _, tc := range []struct {
		value float64
		want  string
	}{
		{100, "100"}, {120, "120"}, {2000, "2000"}, {1, "1"}, {0, "0"},
		{0.5, "0.5"}, {1.25, "1.25"}, {10.1, "10.1"}, {350, "350"},
	} {
		require.Equal(t, tc.want, scalarString(tc.value), "%v", tc.value)
	}
}

// An event that names a namespace must never be answered with another namespace's workload — that is
// the failure this scoping exists to stop, so there is no unscoped retry behind it.
func TestLoadResourceDoesNotFallBackAcrossNamespaces(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dbms := &common.DatabaseManager{Db: sqlx.NewDb(db, "sqlmock")}

	mock.ExpectQuery(`FROM cloud_resourses`).
		WithArgs("acc-1", "llm-server", "", "deployment", "gone").
		WillReturnRows(sqlmock.NewRows([]string{"name", "type", "arn", "last_seen", "meta"}))

	require.Nil(t, loadResource(dbms, "acc-1", "llm-server", "", "deployment", "gone"))
	require.NoError(t, mock.ExpectationsWereMet(), "exactly one query: no unscoped retry")
}
