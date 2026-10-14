package cloud

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"nudgebee/services/config"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAwsAlarmStateFacts(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	since := now.Add(-12 * 24 * time.Hour)

	// A standing alarm: in ALARM for days, never fired before in the window.
	assert.Equal(t, []string{
		"The alarm entered the ALARM state at 2026-10-02T12:00:00Z, 12 days before this evidence was collected.",
		"No earlier firing of this alarm is recorded in the last 7 days.",
	}, awsAlarmStateFacts("ALARM", &since, nil, now))

	// A flapping alarm: fresh state, many earlier firings, newest listed first and bounded.
	fresh := now.Add(-20 * time.Minute)
	var earlier []time.Time
	for i := 1; i <= 7; i++ {
		earlier = append(earlier, now.Add(-time.Duration(i)*time.Hour))
	}
	facts := awsAlarmStateFacts("ALARM", &fresh, earlier, now)
	assert.Equal(t, "The alarm entered the ALARM state at 2026-10-14T11:40:00Z, 20 minutes before this evidence was collected.", facts[0])
	assert.Equal(t, "Earlier firings of this alarm in the last 7 days: 7 (most recent: 2026-10-14T11:00:00Z, 2026-10-14T10:00:00Z, 2026-10-14T09:00:00Z, 2026-10-14T08:00:00Z, 2026-10-14T07:00:00Z).", facts[1])

	// No state time known: only the firing history.
	assert.Equal(t, []string{"No earlier firing of this alarm is recorded in the last 7 days."}, awsAlarmStateFacts("ALARM", nil, nil, now))

	// No state label: generic wording.
	assert.Equal(t, "The alarm entered its current state at 2026-10-02T12:00:00Z, 12 days before this evidence was collected.",
		awsAlarmStateFacts("", &since, nil, now)[0])
}

func TestAwsAlarmStateActionGatesOnAlarmEvents(t *testing.T) {
	a := &cloudAWSAlarmStateAction{}
	alarm := playbooks.NewPlaybookActionContext("tenant-1", "account-1", slog.Default(), playbooks.PlaybookEvent{Labels: map[string]string{
		"aws_event_arn": "arn:aws:cloudwatch:us-east-1:123456789012:alarm:example-cpu-high", "aws_event_state": "ALARM"}})
	assert.True(t, a.CanAutoExecute(alarm))

	cleared := playbooks.NewPlaybookActionContext("tenant-1", "account-1", slog.Default(), playbooks.PlaybookEvent{Labels: map[string]string{
		"aws_event_arn": "arn:aws:cloudwatch:us-east-1:123456789012:alarm:example-cpu-high", "aws_event_state": "OK"}})
	assert.False(t, a.CanAutoExecute(cleared))

	stateChange := playbooks.NewPlaybookActionContext("tenant-1", "account-1", slog.Default(), playbooks.PlaybookEvent{Labels: map[string]string{
		"aws_event_instance": "i-0123456789abcdef0"}})
	assert.False(t, a.CanAutoExecute(stateChange))
}

func TestAwsAlarmStateActionReadsEarlierFirings(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}, nil
	})
	t.Cleanup(func() {
		database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
			return nil, errors.New("no metastore in unit tests")
		})
		_ = rawDB.Close()
	})

	arn := "arn:aws:cloudwatch:us-east-1:123456789012:alarm:example-cpu-high"
	prior := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Second)
	for i := 0; i < 2; i++ {
		mock.ExpectQuery(regexp.QuoteMeta("WHERE labels->>'aws_event_arn' = $1 AND cloud_account_id = $2 AND tenant = $3")).
			WithArgs(arn, "account-1", "tenant-1", sqlmock.AnyArg(), "event-1").
			WillReturnRows(sqlmock.NewRows([]string{"starts_at"}).AddRow(prior))
	}

	detected := time.Now().UTC().Add(-30 * time.Minute)
	event := playbooks.PlaybookEvent{
		EventId: "event-1", DetectedAt: &detected, Source: "AWS_CloudWatch_Alarm",
		Labels: map[string]string{"aws_event_arn": arn, "aws_event_state": "ALARM"}}
	resp, err := (&cloudAWSAlarmStateAction{}).AutoExecute(playbooks.NewPlaybookActionContext("tenant-1", "account-1", slog.Default(), event))
	require.NoError(t, err)
	require.Len(t, resp.GetInsights(), 2)
	assert.Contains(t, resp.GetInsights()[0].Message, "entered the ALARM state")
	assert.Contains(t, resp.GetInsights()[1].Message, "Earlier firings of this alarm in the last 7 days: 1")
	text := resp.GetData().(string)
	assert.True(t, len(text) < 3000)
	assert.Contains(t, text, "### Alarm state")

	// A source whose start may be synthesised at ingest: no state age is claimed.
	event.Source = "pagerduty"
	resp, err = (&cloudAWSAlarmStateAction{}).AutoExecute(playbooks.NewPlaybookActionContext("tenant-1", "account-1", slog.Default(), event))
	require.NoError(t, err)
	require.Len(t, resp.GetInsights(), 1)
	assert.Contains(t, resp.GetInsights()[0].Message, "Earlier firings")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAwsSSMActivityFacts(t *testing.T) {
	since := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)

	// Tagged with a patch group, but nothing ran in the window.
	facts, table := awsSSMActivityFacts([]awsSSMPatchState{{
		Operation: "Install", OperationStartTime: "2026-09-26T02:00:05+00:00", OperationEndTime: "2026-09-26T02:14:40+00:00",
		InstalledCount: 41, MissingCount: 0, FailedCount: 0, PatchGroup: "windows-nonprod",
	}}, nil, since)
	assert.Equal(t, []string{
		`Last patch operation: Install from 2026-09-26T02:00:05Z to 2026-09-26T02:14:40Z (installed 41, missing 0, failed 0), patch group "windows-nonprod".`,
		"No Run Command invocation ran on this instance since 2026-10-07T12:00:00Z.",
	}, facts)
	assert.Empty(t, table)

	// Not managed by patching, with recent commands; times normalised to UTC.
	facts, table = awsSSMActivityFacts(nil, []awsSSMInvocation{
		{DocumentName: "AWS-RunPatchBaseline", RequestedDateTime: "2026-10-12T07:34:21.437000+05:30", Status: "Success"},
		{DocumentName: "AWS-RunShellScript", RequestedDateTime: "2026-10-11T10:00:00+00:00", Status: "Failed"},
	}, since)
	assert.Equal(t, "Systems Manager holds no patch state for this instance.", facts[0])
	assert.Equal(t, "Run Command invocations on this instance since 2026-10-07T12:00:00Z: 2 (most recent: AWS-RunPatchBaseline at 2026-10-12T02:04:21Z, Success).", facts[1])
	assert.Contains(t, table, "| 2026-10-11T10:00:00Z | AWS-RunShellScript | Failed |")

	// A full page says "at least".
	many := make([]awsSSMInvocation, awsSSMInvocationsShown)
	for i := range many {
		many[i] = awsSSMInvocation{DocumentName: "AWS-RunShellScript", RequestedDateTime: "2026-10-12T00:00:00Z", Status: "Success"}
	}
	facts, table = awsSSMActivityFacts(nil, many, since)
	assert.Contains(t, facts[1], ": at least 15 (")
	assert.Less(t, len(table)+len(strings.Join(facts, "")), 3000)
}

func TestAwsSSMActivityActionRunsReadOnlyCommands(t *testing.T) {
	withTenantAccountsForCloudTests(t)
	var commands []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req CloudExecuteCliCommandRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		commands = append(commands, req.Command)
		out := `{"InstancePatchStates": []}`
		if strings.Contains(req.Command, "list-command-invocations") {
			out = `{"CommandInvocations": [{"DocumentName": "AWS-RunShellScript", "RequestedDateTime": "2026-10-13T09:00:00+00:00", "Status": "Success"}]}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	t.Cleanup(srv.Close)
	prevURL := config.Config.CloudCollectorServerUrl
	config.Config.CloudCollectorServerUrl = srv.URL
	t.Cleanup(func() { config.Config.CloudCollectorServerUrl = prevURL })

	detected := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	ctx := playbooks.NewPlaybookActionContext("tenant-1", "account-1", slog.Default(), playbooks.PlaybookEvent{
		DetectedAt: &detected, Labels: map[string]string{"aws_event_instance": "i-0123456789abcdef0", "aws_region": "us-east-1"}})
	a := &cloudAWSSSMActivityAction{}
	require.True(t, a.CanAutoExecute(ctx))
	resp, err := a.AutoExecute(ctx)
	require.NoError(t, err)
	require.NotNil(t, resp)

	require.Len(t, commands, 2)
	assert.Equal(t, "aws ssm describe-instance-patch-states --instance-ids 'i-0123456789abcdef0' --region 'us-east-1' --output json", commands[0])
	assert.Equal(t, "aws ssm list-command-invocations --instance-id 'i-0123456789abcdef0' --filters key=InvokedAfter,value=2026-10-07T12:00:00Z --max-items 15 --region 'us-east-1' --output json", commands[1])
	assert.Contains(t, resp.GetInsights()[1].Message, "most recent: AWS-RunShellScript at 2026-10-13T09:00:00Z, Success")
	assert.Contains(t, resp.GetData().(string), "### Systems Manager activity")

	// Past the enrichment deadline: no cloud call, no evidence.
	before := len(commands)
	late := playbooks.NewPlaybookActionContext("tenant-1", "account-1", slog.Default(), playbooks.PlaybookEvent{
		DetectedAt: &detected, Deadline: time.Now().Add(-time.Second),
		Labels: map[string]string{"aws_event_instance": "i-0123456789abcdef0", "aws_region": "us-east-1"}})
	resp, err = a.AutoExecute(late)
	require.NoError(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, before, len(commands))

	// An alarm back to OK does not need it.
	assert.False(t, a.CanAutoExecute(playbooks.NewPlaybookActionContext("tenant-1", "account-1", slog.Default(), playbooks.PlaybookEvent{
		Labels: map[string]string{"aws_event_instance": "i-0123456789abcdef0", "aws_region": "us-east-1", "aws_event_state": "OK"}})))

	// Not an instance alarm: the action does not run.
	assert.False(t, a.CanAutoExecute(playbooks.NewPlaybookActionContext("tenant-1", "account-1", slog.Default(), playbooks.PlaybookEvent{
		Labels: map[string]string{"aws_event_instance": "orders-db", "aws_region": "us-east-1"}})))
}

// withTenantAccountsForCloudTests backs the tenant-admin request context the
// cloud actions build, which reads the tenant's account ids from the metastore.
func withTenantAccountsForCloudTests(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 4; i++ {
		mock.ExpectQuery("SELECT id FROM cloud_accounts WHERE tenant").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("account-1"))
	}
	database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
		return &database.DatabaseManager{Db: sqlx.NewDb(rawDB, "postgresql")}, nil
	})
	t.Cleanup(func() {
		database.RegisterDatabaseManagerHook(database.Metastore, func() (*database.DatabaseManager, error) {
			return nil, errors.New("no metastore in unit tests")
		})
		_ = rawDB.Close()
	})
}
