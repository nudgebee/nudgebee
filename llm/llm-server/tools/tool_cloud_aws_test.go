package tools

import (
	"errors"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"nudgebee/llm/workspace"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsShellSyntax(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		expected bool
	}{
		// Legitimate AWS CLI commands — should NOT be flagged
		{
			name:     "simple aws command",
			command:  "aws ec2 describe-instances --region us-east-1",
			expected: false,
		},
		{
			name:     "JMESPath query with && and ||",
			command:  `aws cloudwatch describe-alarms --region eu-west-1 --query 'MetricAlarms[?Dimensions[?Name==` + "`InstanceId`" + ` && Value==` + "`i-09c7ef246af8d88aa`" + `] && (MetricName==` + "`StatusCheckFailed`" + ` || MetricName==` + "`StatusCheckFailed_Instance`" + `)].[AlarmName, MetricName, StateValue]' --output table`,
			expected: false,
		},
		{
			name:     "JMESPath with double-quoted string containing &&",
			command:  `aws ec2 describe-instances --query "Reservations[*].Instances[?State.Name=='running' && Tags[?Key=='Env']]"`,
			expected: false,
		},
		{
			name:     "JMESPath with || in single quotes",
			command:  `aws ec2 describe-instances --query 'Reservations[*].Instances[?State.Name==` + "`running`" + ` || State.Name==` + "`stopped`" + `]'`,
			expected: false,
		},
		{
			name:     "simple filter with single quotes",
			command:  `aws ec2 describe-instances --filters "Name=tag:Environment,Values=production"`,
			expected: false,
		},

		// Shell syntax — SHOULD be flagged
		{
			name:     "command chaining with &&",
			command:  "aws s3 ls && aws ec2 describe-instances",
			expected: true,
		},
		{
			name:     "command chaining with ||",
			command:  "aws s3 ls || echo failed",
			expected: true,
		},
		{
			name:     "command substitution with $()",
			command:  "aws ec2 describe-instances --instance-ids $(aws ec2 describe-instances --query 'Reservations[*].Instances[*].InstanceId' --output text)",
			expected: true,
		},
		{
			name:     "for loop",
			command:  "for i in 1 2 3; do aws s3 ls s3://bucket-$i; done",
			expected: true,
		},
		{
			name:     "while loop",
			command:  "while true; do aws sqs receive-message --queue-url q; done",
			expected: true,
		},
		{
			name:     "if then conditional",
			command:  "if aws s3 ls s3://bucket; then echo exists; fi",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isShellSyntax(tt.command)
			assert.Equal(t, tt.expected, result, "command: %s", tt.command)
		})
	}
}

func TestStripQuotedContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "no quotes",
			input:    "aws ec2 describe-instances",
			expected: "aws ec2 describe-instances",
		},
		{
			name:     "single-quoted content stripped",
			input:    "aws --query 'foo && bar'",
			expected: "aws --query ''",
		},
		{
			name:     "double-quoted content stripped",
			input:    `aws --query "foo && bar"`,
			expected: `aws --query ""`,
		},
		{
			name:     "mixed quotes",
			input:    `aws --query 'foo && bar' --filter "baz || qux"`,
			expected: `aws --query '' --filter ""`,
		},
		{
			name:     "unquoted && preserved",
			input:    "aws s3 ls && aws ec2 list",
			expected: "aws s3 ls && aws ec2 list",
		},
		{
			name:     "escaped quote in double quotes",
			input:    `aws --query "foo \"&&\" bar"`,
			expected: `aws --query ""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StripQuotedContent(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// isShellSyntax detects a substitution by the two-character "$(", so preserving a lone "$" through
// the quote strip is not enough for it — "$(id)" inside double quotes became "$" and matched
// nothing. A backtick substitution was never in its pattern list at all, so it bypassed the guard
// whether quoted or not.
//
// This function currently has no production callers; it is kept correct so that wiring it up later
// is not a trap.
func TestIsShellSyntax_SubstitutionInsideDoubleQuotes(t *testing.T) {
	for _, tt := range []struct {
		name    string
		command string
		want    bool
	}{
		{"command substitution in double quotes", `aws s3 ls "$(id)"`, true},
		{"backtick substitution in double quotes", "aws s3 ls \"`id`\"", true},
		{"backtick substitution unquoted", "aws s3 ls `id`", true},
		{"command substitution unquoted", `aws s3 ls $(id)`, true},
		{"single quotes suppress substitution", `aws s3 ls '$(id)'`, false},
		{"jmespath filter is not shell syntax", `aws ec2 describe-instances --query "Reservations[].Instances[?State.Name=='running']"`, false},
		{"parenthesised jmespath is not shell syntax", `aws ec2 describe-instances --query "length(Reservations)"`, false},
		{"plain command", `aws ec2 describe-instance-status --instance-ids i-0abc`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isShellSyntax(tt.command))
		})
	}
}

// metricSaveWorkspace runs every command with a fixed stdout and records (or fails) SaveFile.
type metricSaveWorkspace struct {
	workspace.WorkspaceManager
	stdout          string
	saveErr         error
	savedPath, body string
	saves           int
}

func (m *metricSaveWorkspace) ExecuteOrLazyCreate(_ *security.RequestContext, _, _, _ string, _ map[string]string) (string, error) {
	return m.stdout, nil
}

func (m *metricSaveWorkspace) SaveFile(_ *security.RequestContext, _, _, path, content string) error {
	m.saves++
	if m.saveErr != nil {
		return m.saveErr
	}
	m.savedPath, m.body = path, content
	return nil
}

func TestIsCloudMetricRead_Aws(t *testing.T) {
	tests := []struct {
		command string
		want    bool
	}{
		{"aws cloudwatch get-metric-data --metric-data-queries file://q.json --start-time 2026-10-13T00:00:00Z --end-time 2026-10-20T00:00:00Z", true},
		{"aws cloudwatch get-metric-statistics --namespace AWS/RDS --metric-name CPUUtilization --period 300 --statistics Average", true},
		{"aws --profile prod --region us-east-1 cloudwatch get-metric-data --metric-data-queries '[{\"Id\":\"m1\"}]'", true},
		{"aws --region=us-east-1 cloudwatch get-metric-statistics --namespace AWS/EC2", true},
		{"aws --no-cli-pager cloudwatch get-metric-data --cli-input-json file://in.json", true},
		{"aws cloudwatch get-metric-data --query 'MetricDataResults[?Id==`m1` && Label!=`x`]'", true},
		{"AWS CloudWatch Get-Metric-Data --metric-data-queries file://q.json", true},
		{"aws cloudwatch describe-alarms --state-value ALARM", false},
		{"aws cloudwatch list-metrics --namespace AWS/EC2", false},
		{"aws cloudwatch get-metric-widget-image --metric-widget file://w.json", false},
		{"aws ec2 describe-instances --region us-east-1", false},
		{"aws logs get-log-events --log-group-name g", false},
		{"aws cloudwatch get-metric-data --metric-data-queries file://q.json | jq .", false},
		{"aws cloudwatch get-metric-data --metric-data-queries file://q.json > out.json", false},
		{"aws cloudwatch get-metric-data --metric-data-queries file://q.json && echo done", false},
		{"aws cloudwatch get-metric-data --start-time $(date -d '-7 days' +%s)", false},
		{"cloudwatch get-metric-data --metric-data-queries file://q.json", false},
		{"az monitor metrics list --resource r", false},
		{"", false},
		{"aws cloudwatch get-metric-data 'unterminated", false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, isCloudMetricRead(awsMetricRead, tt.command), tt.command)
	}
}

const cloudWatchMetricJSON = `{"MetricDataResults":[{"Id":"m1","Label":"CPUUtilization","Timestamps":["2026-10-20T00:00:00Z","2026-10-19T23:55:00Z"],"Values":[42.1,40.3],"StatusCode":"Complete"}]}`

func awsMetricCall(t *testing.T, ws *metricSaveWorkspace, command string) core.NBToolResponse {
	t.Helper()
	tool := AwsCliTool{workspaceManager: ws}
	resp, err := tool.Call(cloudCliToolContext("workspace", "aws-id", "aws-prod", tool), core.NBToolCallRequest{Command: command})
	require.NoError(t, err)
	require.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)
	return resp
}

func TestAwsCall_MetricReadSavedWithRefAndNote(t *testing.T) {
	ws := &metricSaveWorkspace{stdout: cloudWatchMetricJSON + "\n"}
	resp := awsMetricCall(t, ws, "aws cloudwatch get-metric-data --metric-data-queries file://q.json --start-time 2026-10-13T00:00:00Z --end-time 2026-10-20T00:00:00Z")

	require.Equal(t, 1, ws.saves)
	assert.True(t, strings.HasPrefix(ws.savedPath, "metrics_aws_cloudwatch_"), ws.savedPath)
	assert.True(t, strings.HasSuffix(ws.savedPath, ".json"))
	assert.Equal(t, cloudWatchMetricJSON, ws.body, "the raw CLI JSON is saved")
	require.Len(t, resp.References, 1)
	assert.Equal(t, core.NBToolResponseReference{Type: "file", Url: ws.savedPath, Text: ws.savedPath, Description: awsMetricRead.description}, resp.References[0])

	want := "[account: aws-prod]\n" + cloudWatchMetricJSON + "\n"
	require.True(t, strings.HasPrefix(resp.Data, want), "the output the model sees is otherwise unchanged: %s", resp.Data)
	note := strings.TrimPrefix(resp.Data, want)
	assert.Equal(t, 1, strings.Count(strings.TrimSpace(note), "\n")+1, "one line added")
	assert.Contains(t, note, `"`+ws.savedPath+`"`)
	assert.Contains(t, note, "analyze_metric_series")
}

func TestAwsCall_NonMetricOrNonJSONNotSaved(t *testing.T) {
	for _, tt := range []struct{ name, command, stdout string }{
		{"other command", "aws cloudwatch describe-alarms", `{"MetricAlarms":[]}`},
		{"text output", "aws cloudwatch get-metric-statistics --namespace AWS/EC2 --output text", "DATAPOINTS 42.1 2026-10-20T00:00:00Z Percent"},
		{"empty output", "aws cloudwatch get-metric-data --metric-data-queries file://q.json", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ws := &metricSaveWorkspace{stdout: tt.stdout}
			resp := awsMetricCall(t, ws, tt.command)
			assert.Equal(t, 0, ws.saves)
			assert.Empty(t, resp.References)
			assert.Equal(t, "[account: aws-prod]\n"+tt.stdout, resp.Data)
		})
	}
}

func TestAwsCall_MetricSaveFailureKeepsNormalOutput(t *testing.T) {
	ws := &metricSaveWorkspace{stdout: cloudWatchMetricJSON, saveErr: errors.New("workspace unavailable")}
	resp := awsMetricCall(t, ws, "aws cloudwatch get-metric-data --metric-data-queries file://q.json")
	assert.Equal(t, 1, ws.saves)
	assert.Empty(t, resp.References)
	assert.Equal(t, "[account: aws-prod]\n"+cloudWatchMetricJSON, resp.Data)
}

// Seen on production (#42168): an alert on a us-west-2 server was looked up
// with the CLI's default region, and AWS answered "instance does not exist".
// The alert's region now reaches a read on the alert's account.
func TestAwsReadUsesTheAlertsRegion(t *testing.T) {
	own := awsCfg("aa-prod-app", "prod-id", "111111111111")
	other := awsCfg("aa-dmz-app", "dmz-id", "222222222222")
	alert := core.NbToolContext{AccountId: "prod-id", QueryConfig: core.NBQueryConfig{Labels: map[string]any{"region": "us-west-2"}}}

	command, note := withEventRegion(alert, own, "aws ec2 describe-instances --instance-ids i-01c2485e1f37cf620")
	assert.Equal(t, "aws --region us-west-2 ec2 describe-instances --instance-ids i-01c2485e1f37cf620", command)
	assert.Equal(t, "region: us-west-2 (the alert's region)", note)

	command, note = withEventRegion(alert, own, "aws ec2 describe-instances --region us-east-1")
	assert.Equal(t, "aws ec2 describe-instances --region us-east-1", command, "a region the call names is kept")
	assert.Equal(t, "region: us-east-1; the alert is in us-west-2", note, "and the step says it differs from the alert's")

	command, note = withEventRegion(alert, own, "aws ec2 describe-instances --region=us-west-2")
	assert.Equal(t, "aws ec2 describe-instances --region=us-west-2", command)
	assert.Empty(t, note, "the alert's own region needs no note")

	command, note = withEventRegion(alert, other, "aws ec2 describe-instances")
	assert.Equal(t, "aws ec2 describe-instances", command, "another account's call is not given the alert's region")
	assert.Empty(t, note)

	command, note = withEventRegion(alert, own, "aws ec2 stop-instances --instance-ids i-01c2485e1f37cf620")
	assert.Equal(t, "aws ec2 stop-instances --instance-ids i-01c2485e1f37cf620", command, "a write runs as approved")
	assert.Empty(t, note)
}

// A webhook event names its AWS account by number; the alert's region follows
// that account, not the conversation's.
func TestAwsAlertRegionFollowsTheEventsAccountNumber(t *testing.T) {
	ctx := core.NbToolContext{AccountId: "k8s-id", QueryConfig: core.NBQueryConfig{Labels: map[string]any{
		"nb_cloud_account_id": "111111111111", "nb_cloud_region": "eu-west-1", "region": "us-east-1",
	}}}
	assert.Equal(t, "eu-west-1", eventAwsRegion(ctx, awsCfg("aa-prod-app", "prod-id", "111111111111")), "nb_cloud_region wins")
	assert.Empty(t, eventAwsRegion(ctx, awsCfg("aa-dmz-app", "dmz-id", "222222222222")))
}

func TestAwsAlertRegionIgnoresWhatIsNotARegion(t *testing.T) {
	own := awsCfg("aa-prod-app", "prod-id", "111111111111")
	for _, value := range []any{"", "us-east-1; rm -rf /", "global", 42, "US-EAST-1"} {
		ctx := core.NbToolContext{AccountId: "prod-id", QueryConfig: core.NBQueryConfig{Labels: map[string]any{"region": value}}}
		assert.Empty(t, eventAwsRegion(ctx, own), "%v", value)
	}
	assert.Empty(t, eventAwsRegion(core.NbToolContext{AccountId: "prod-id"}, own), "no labels")
	ctx := core.NbToolContext{AccountId: "prod-id", QueryConfig: core.NBQueryConfig{Labels: map[string]any{"region": "us-gov-west-1"}}}
	assert.Equal(t, "us-gov-west-1", eventAwsRegion(ctx, own))
}

// Seen on production (#42168): Nubi tried i-036cf0f273a916799 in three
// accounts in turn; the inventory knew it was in a fourth.
func TestAwsInstanceNotFoundSaysWhereItIs(t *testing.T) {
	prevLookup, prevRegions := awsInstanceLocations, awsInstanceRegions
	t.Cleanup(func() { awsInstanceLocations, awsInstanceRegions = prevLookup, prevRegions })
	awsInstanceLocations = func(_ *security.RequestContext, ids []string) ([]awsInstanceLocation, error) {
		return []awsInstanceLocation{
			{ID: "i-036cf0f273a916799", Region: "us-east-1", AccountID: "security-id", AccountName: "aa-prod-security"},
			{ID: "i-0aaaaaaaaaaaaaaaa", Region: "eu-west-1", AccountID: "hidden-id", AccountName: "hidden"},
		}, nil
	}
	awsInstanceRegions = func(account string) ([]string, error) {
		assert.Equal(t, "prod-id", account, "regions of the account the command ran on")
		return []string{"us-east-1", "us-west-2"}, nil
	}
	ctx := core.NbToolContext{Ctx: security.NewRequestContextForTenantAccountAdmin("tenant", "user", []string{"prod-id", "security-id"}), AccountId: "prod-id"}
	ran := awsCfg("aa-prod-app", "prod-id", "111111111111")

	note := instanceNotFoundNote(ctx, ran, "An error occurred (InvalidInstanceID.NotFound) when calling the DescribeInstances operation: The instance ID 'i-036cf0f273a916799' does not exist")
	assert.Contains(t, note, "i-036cf0f273a916799 is in account aa-prod-security, region us-east-1: run again with account=aa-prod-security and --region us-east-1.")
	assert.NotContains(t, note, "inventory has no")

	note = instanceNotFoundNote(ctx, ran, "An error occurred (InvalidInstanceID.NotFound) when calling the DescribeInstances operation: The instance IDs 'i-0aaaaaaaaaaaaaaaa, i-0bbbbbbbbbbbbbbbb' do not exist")
	assert.NotContains(t, note, "hidden", "an account the caller cannot read is not named")
	assert.Contains(t, note, "NudgeBee's inventory has no i-0aaaaaaaaaaaaaaaa, i-0bbbbbbbbbbbbbbbb in an account you can read.")
	assert.Contains(t, note, "Account aa-prod-app has EC2 instances only in us-east-1, us-west-2")

	assert.Empty(t, instanceNotFoundNote(ctx, ran, "An error occurred (AccessDenied) when calling the DescribeInstances operation"), "other errors get no note")
	assert.Empty(t, instanceNotFoundNote(ctx, ran, `{"Reservations": [{"Instances": [{"InstanceId": "i-036cf0f273a916799"}]}]}`), "a successful read gets no note")
}
