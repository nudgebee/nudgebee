package aws

import (
	"context"
	"strings"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
)

// fakeAwsProviderAPI captures whatever QueryMetricsRequest executeAction
// builds, without making a real AWS call — everything else is a stub, since
// the get_cloudwatch_metric_data_on_alarm action only calls QueryMetrices.
type fakeAwsProviderAPI struct {
	capturedFilter providers.QueryMetricsRequest
	called         bool
}

func (f *fakeAwsProviderAPI) QueryLogs(providers.CloudProviderContext, providers.Account, providers.QueryLogsRequest) (providers.QueryLogsResponse, error) {
	return providers.QueryLogsResponse{}, nil
}
func (f *fakeAwsProviderAPI) QueryMetrices(_ providers.CloudProviderContext, _ providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error) {
	f.capturedFilter = filter
	f.called = true
	return providers.QueryMetricsResponse{}, nil
}
func (f *fakeAwsProviderAPI) ListResources(providers.CloudProviderContext, providers.Account, providers.ListResourceRequest) (providers.ListResourcesResponse, error) {
	return providers.ListResourcesResponse{}, nil
}
func (f *fakeAwsProviderAPI) GetECSServiceDetails(providers.CloudProviderContext, providers.Account, string, string, string) (providers.Resource, error) {
	return providers.Resource{}, nil
}
func (f *fakeAwsProviderAPI) GetECSTaskDefinitionDetails(providers.CloudProviderContext, providers.Account, string, string) (providers.Resource, error) {
	return providers.Resource{}, nil
}
func (f *fakeAwsProviderAPI) LookupCloudTrailEvents(providers.CloudProviderContext, providers.Account, string, *cloudtrail.LookupEventsInput) (*cloudtrail.LookupEventsOutput, error) {
	return nil, nil
}

// TestReproExpressionAlarmEvidence runs the exact reproduction: an alarm with
// three metricStat inputs (sent, current, deleted) and one expression as the
// alarm's real value (ReturnData=true on the expression, false on the raw
// inputs), driven through the real, unmodified pipeline — GetEventRules loads
// the actual embedded aws_runbook.yaml, Process runs real rule-matching and
// action execution — with only the final AWS network call faked out. It
// checks whether the evidence-fetch request built by get_cloudwatch_metric_
// data_on_alarm carries current/deleted/the expression, or only "sent".
func TestReproExpressionAlarmEvidence(t *testing.T) {
	rules, err := GetEventRules("")
	if err != nil {
		t.Fatalf("failed to load embedded aws_runbook.yaml: %v", err)
	}

	fake := &fakeAwsProviderAPI{}
	processor := NewTemplatedEventBridgeProcessor(rules, fake)
	pCtx := providers.NewCloudProviderContext(context.Background())

	alarmDetail := `{
  "alarmName": "nb-demo-remaining",
  "state": {"value": "ALARM", "reason": "Threshold Crossed", "timestamp": "2026-09-16T04:37:01.389+0000"},
  "previousState": {"value": "OK"},
  "configuration": {
    "metrics": [
      {"id": "m1", "metricStat": {"stat": "Sum", "period": 60, "metric": {"namespace": "AWS/Custom", "name": "sent", "dimensions": {"QueueName": "orders"}}}, "returnData": false},
      {"id": "m2", "metricStat": {"stat": "Sum", "period": 60, "metric": {"namespace": "AWS/Custom", "name": "current", "dimensions": {"QueueName": "orders"}}}, "returnData": false},
      {"id": "m3", "metricStat": {"stat": "Sum", "period": 60, "metric": {"namespace": "AWS/Custom", "name": "deleted", "dimensions": {"QueueName": "orders"}}}, "returnData": false},
      {"id": "e1", "expression": "m1 - m2 - m3", "label": "Remaining", "returnData": true}
    ]
  }
}`

	ebEvent := EventBridgeEvent{
		ID:         "repro-evidence",
		Source:     "aws.cloudwatch",
		Region:     "us-east-1",
		Account:    "123456789012",
		DetailType: "CloudWatch Alarm State Change",
		Time:       time.Date(2026, 9, 16, 4, 37, 1, 0, time.UTC),
		Detail:     []byte(alarmDetail),
	}

	account := providers.Account{AccountNumber: "123456789012", AccountName: "repro"}

	provEvent, err := processor.Process(pCtx, ebEvent, account)
	if err != nil {
		t.Fatalf("Process returned an error: %v", err)
	}

	if !strings.Contains(provEvent.Description, "Metric: Remaining (m1 - m2 - m3)") {
		t.Errorf("description = %q, want it to name the expression (Remaining (m1 - m2 - m3)), not a raw input", provEvent.Description)
	}

	if !fake.called {
		t.Fatal("get_cloudwatch_metric_data_on_alarm never called QueryMetrices — the rule likely didn't match")
	}

	filter := fake.capturedFilter

	if len(filter.MetricQueries) == 0 {
		t.Fatalf("REPRODUCED THE BUG: filter.MetricQueries is empty. "+
			"Evidence-fetch fell back to the single-metric path: MetricNamespace=%q MetricNames=%v — "+
			"only one series (%v) would be fetched, current/deleted/the expression are never queried.",
			filter.MetricNamespace, filter.MetricNames, filter.MetricNames)
	}

	gotIds := map[string]bool{}
	var expressionSeen string
	for _, mq := range filter.MetricQueries {
		gotIds[mq.Id] = true
		if mq.Expression != "" {
			expressionSeen = mq.Expression
		}
	}

	for _, want := range []string{"m1", "m2", "m3", "e1"} {
		if !gotIds[want] {
			t.Errorf("filter.MetricQueries is missing id %q — got ids: %v", want, gotIds)
		}
	}
	if expressionSeen != "m1 - m2 - m3" {
		t.Errorf("expression in the built request = %q, want %q — the alarm's real value would not be fetched", expressionSeen, "m1 - m2 - m3")
	}

	t.Logf("PASS: evidence-fetch request carries all 4 entries (sent, current, deleted, and the expression %q) instead of just \"sent\".", expressionSeen)
}

// TestReproExpressionFirstStillFetchesData covers the acceptance criterion
// directly: "an alarm whose metrics[0] is the expression still fetches data
// and never renders an empty metric name." Same alarm as
// TestReproExpressionAlarmEvidence, but with the expression listed FIRST in
// the array — this is the exact ordering the original index-0 bug choked on
// (metrics[0] having no metricStat meant namespace/metric_name rendered
// blank). Runs through the real, unmodified Process() pipeline, same as
// above, with only the AWS network call faked out.
func TestReproExpressionFirstStillFetchesData(t *testing.T) {
	rules, err := GetEventRules("")
	if err != nil {
		t.Fatalf("failed to load embedded aws_runbook.yaml: %v", err)
	}

	fake := &fakeAwsProviderAPI{}
	processor := NewTemplatedEventBridgeProcessor(rules, fake)
	pCtx := providers.NewCloudProviderContext(context.Background())

	// Expression (e1) is metrics[0] here — the raw inputs come after it.
	alarmDetail := `{
  "alarmName": "nb-demo-remaining-expr-first",
  "state": {"value": "ALARM", "reason": "Threshold Crossed", "timestamp": "2026-09-16T04:37:01.389+0000"},
  "previousState": {"value": "OK"},
  "configuration": {
    "metrics": [
      {"id": "e1", "expression": "m1 - m2 - m3", "label": "Remaining", "returnData": true},
      {"id": "m1", "metricStat": {"stat": "Sum", "period": 60, "metric": {"namespace": "AWS/Custom", "name": "sent", "dimensions": {"QueueName": "orders"}}}, "returnData": false},
      {"id": "m2", "metricStat": {"stat": "Sum", "period": 60, "metric": {"namespace": "AWS/Custom", "name": "current", "dimensions": {"QueueName": "orders"}}}, "returnData": false},
      {"id": "m3", "metricStat": {"stat": "Sum", "period": 60, "metric": {"namespace": "AWS/Custom", "name": "deleted", "dimensions": {"QueueName": "orders"}}}, "returnData": false}
    ]
  }
}`

	ebEvent := EventBridgeEvent{
		ID:         "repro-evidence-expr-first",
		Source:     "aws.cloudwatch",
		Region:     "us-east-1",
		Account:    "123456789012",
		DetailType: "CloudWatch Alarm State Change",
		Time:       time.Date(2026, 9, 16, 4, 37, 1, 0, time.UTC),
		Detail:     []byte(alarmDetail),
	}
	account := providers.Account{AccountNumber: "123456789012", AccountName: "repro"}

	provEvent, err := processor.Process(pCtx, ebEvent, account)
	if err != nil {
		t.Fatalf("Process returned an error: %v", err)
	}

	// "Never renders an empty metric name" — check the actual alert description
	// text the event_template built, not just the action's params. This is
	// the field a human reads; a blank "Metric: ." here is the literal bug.
	// With the expression entry present, the description now names the
	// expression and its formula (Remaining (m1 - m2 - m3)) rather than
	// falling back to one arbitrary raw input — the alert should say what
	// actually alarmed, not just avoid rendering blank.
	if strings.Contains(provEvent.Description, "Metric: .") || strings.Contains(provEvent.Description, "Namespace: .") {
		t.Errorf("description rendered an empty namespace/metric name: %q", provEvent.Description)
	}
	if !strings.Contains(provEvent.Description, "Metric: Remaining (m1 - m2 - m3)") {
		t.Errorf("description = %q, want it to name the expression (Remaining (m1 - m2 - m3)), not a raw input", provEvent.Description)
	}

	if !fake.called {
		t.Fatal("get_cloudwatch_metric_data_on_alarm never called QueryMetrices")
	}

	// "Still fetches data" — the evidence-fetch request must carry all 4
	// entries, same as the other ordering, proving the fix is order-independent.
	filter := fake.capturedFilter
	if len(filter.MetricQueries) != 4 {
		t.Fatalf("filter.MetricQueries has %d entries, want 4 (metrics[0] being the expression must not lose data): %+v",
			len(filter.MetricQueries), filter.MetricQueries)
	}
	gotIds := map[string]bool{}
	for _, mq := range filter.MetricQueries {
		gotIds[mq.Id] = true
	}
	for _, want := range []string{"m1", "m2", "m3", "e1"} {
		if !gotIds[want] {
			t.Errorf("filter.MetricQueries is missing id %q with metrics[0]=expression — got: %v", want, gotIds)
		}
	}

	t.Logf("PASS: metrics[0]=expression still fetched all 4 entries and rendered a non-empty description: %q", provEvent.Description)
}
