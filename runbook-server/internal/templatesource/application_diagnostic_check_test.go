package templatesource

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"nudgebee/runbook/internal/model"
	"nudgebee/runbook/internal/tasks/data"
	"nudgebee/runbook/internal/tasks/testutils"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/internal/workflow"

	temporallog "go.temporal.io/sdk/log"
)

// The Application Diagnostic Check template is the only bundled template that renders
// task params from other tasks' outputs and then reduces them to a verdict, so a
// "definition parses" assertion would prove very little. These tests render the real
// params through the real templating engine and run the real data.transform task, which
// is what catches the two failure modes this template can actually hit: a param that
// blows up when an optional collector is skipped, and a verdict that misreads a payload.

const diagnosticTemplateSlug = "application_diagnostic_check"

func loadDiagnosticDefinition(t *testing.T) model.WorkflowDefinition {
	t.Helper()

	raw, err := bundledTemplates.ReadFile("templates/" + diagnosticTemplateSlug + ".yaml")
	if err != nil {
		t.Fatalf("failed to read bundled template: %v", err)
	}
	rt, err := decode(raw, validTaskTypes())
	if err != nil {
		t.Fatalf("template failed to decode: %v", err)
	}
	var def model.WorkflowDefinition
	if err := json.Unmarshal([]byte(rt.DefinitionJSON), &def); err != nil {
		t.Fatalf("definition is not a valid workflow: %v", err)
	}
	return def
}

func taskByID(t *testing.T, def model.WorkflowDefinition, id string) model.Task {
	t.Helper()
	for _, task := range def.Tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("task %q not found in definition", id)
	return model.Task{}
}

func testTaskContext() types.TaskContext {
	logger := temporallog.NewStructuredLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	return testutils.NewTestTaskContext("test-tenant", "test-account", "test-user", logger)
}

// runTransform renders a data.transform task's params against ctx and executes it,
// returning the task's `data` field.
func runTransform(t *testing.T, task model.Task, ctx *workflow.TemplateContext) any {
	t.Helper()

	rendered, err := workflow.ProcessValue(task.Params, ctx)
	if err != nil {
		t.Fatalf("failed to render params for %q: %v", task.ID, err)
	}
	params, ok := rendered.(map[string]any)
	if !ok {
		t.Fatalf("rendered params for %q are %T, want map", task.ID, rendered)
	}

	out, err := (&data.TransformTask{}).Execute(testTaskContext(), params)
	if err != nil {
		t.Fatalf("%q failed to execute: %v", task.ID, err)
	}
	result, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("%q returned %T, want map", task.ID, out)
	}
	return result["data"]
}

func TestDiagnosticTemplate_DAGIsWellFormed(t *testing.T) {
	def := loadDiagnosticDefinition(t)

	ids := make(map[string]bool, len(def.Tasks))
	for _, task := range def.Tasks {
		if ids[task.ID] {
			t.Errorf("duplicate task id %q", task.ID)
		}
		ids[task.ID] = true
	}
	for _, task := range def.Tasks {
		for _, dep := range task.DependsOn {
			if !ids[dep] {
				t.Errorf("task %q depends on unknown task %q", task.ID, dep)
			}
		}
	}

	// A skipped dependency propagates the skip to any dependent that has no `if` of its
	// own (internal/workflow/executor.go). The aggregator depends on every optional
	// collector, so without its own `if` it would be skipped whenever one is — and the
	// workflow would produce no result at all.
	if got := taskByID(t, def, "diagnostic_result").If; got == "" {
		t.Error("diagnostic_result must set `if` so a skipped optional collector does not skip the aggregator")
	}

	if len(def.Output) == 0 {
		t.Error("definition must declare a structured output")
	}
	for _, key := range []string{"status", "summary", "target", "findings", "checks", "coverage", "evidence"} {
		if _, ok := def.Output[key]; !ok {
			t.Errorf("definition output is missing %q", key)
		}
	}
}

func TestDiagnosticTemplate_ResolvesTargetByInstanceIDOrApplication(t *testing.T) {
	def := loadDiagnosticDefinition(t)
	resolve := taskByID(t, def, "resolve_target")

	cases := []struct {
		name       string
		instanceID string
		app        string
		want       string
		notWant    string
	}{
		{
			name:       "instance id wins",
			instanceID: "i-0abc123",
			app:        "payments-api",
			want:       "--instance-ids i-0abc123",
			notWant:    "--filters",
		},
		{
			name:    "falls back to the Name tag",
			app:     "payments api",
			want:    `--filters Name=tag:Name,Values="payments api"`,
			notWant: "--instance-ids",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := workflow.NewTemplateContext(nil, nil)
			ctx.Inputs["region"] = "us-east-1"
			ctx.Inputs["instance_id"] = tc.instanceID
			ctx.Inputs["application"] = tc.app

			rendered, err := workflow.ProcessValue(resolve.Params, ctx)
			if err != nil {
				t.Fatalf("failed to render resolve_target: %v", err)
			}
			command, _ := rendered.(map[string]any)["command"].(string)
			if !strings.Contains(command, tc.want) {
				t.Errorf("command %q does not contain %q", command, tc.want)
			}
			if strings.Contains(command, tc.notWant) {
				t.Errorf("command %q unexpectedly contains %q", command, tc.notWant)
			}
			// The cloud-collector ForkExecs the command instead of running it in a
			// shell, so a substitution here would reach the AWS CLI as a literal.
			if strings.Contains(command, "$(") {
				t.Errorf("command %q uses shell substitution, which the collector does not evaluate", command)
			}
		})
	}
}

func TestDiagnosticTemplate_TimeWindowFeedsCloudWatchCommands(t *testing.T) {
	def := loadDiagnosticDefinition(t)

	ctx := workflow.NewTemplateContext(nil, nil)
	ctx.Inputs["lookback"] = "2h"
	window, ok := runTransform(t, taskByID(t, def, "time_window"), ctx).(map[string]any)
	if !ok {
		t.Fatalf("time_window returned %T, want map", window)
	}
	for _, key := range []string{"start_iso", "end_iso", "start_ms", "end_ms"} {
		if window[key] == nil || window[key] == "" {
			t.Errorf("time_window is missing %q", key)
		}
	}

	ctx.Inputs["region"] = "us-east-1"
	ctx.Inputs["log_group"] = "/aws/ec2/payments-api"
	ctx.Inputs["log_filter"] = "ERROR"
	ctx.Tasks["time_window"] = map[string]any{"status": "completed", "output": map[string]any{"data": window}}
	ctx.Tasks["select_instance"] = map[string]any{
		"status": "completed",
		"output": map[string]any{"data": map[string]any{"instance_id": "i-0abc123"}},
	}

	for _, id := range []string{"cw_cpu", "cw_status_check", "app_logs"} {
		rendered, err := workflow.ProcessValue(taskByID(t, def, id).Params, ctx)
		if err != nil {
			t.Fatalf("failed to render %q: %v", id, err)
		}
		command, _ := rendered.(map[string]any)["command"].(string)
		if strings.Contains(command, "{{") || strings.Contains(command, "$(") {
			t.Errorf("%s left an unresolved expression: %q", id, command)
		}
		if !strings.Contains(command, "--start-time") || !strings.Contains(command, "--end-time") {
			t.Errorf("%s is missing a bounded time window: %q", id, command)
		}
	}
}

// A templating error in any single task fails the whole run at execution time, which is
// the worst moment to find one. Render every task's params once against a fully populated
// context so an unresolvable expression shows up here instead.
func TestDiagnosticTemplate_EveryTaskParamRenders(t *testing.T) {
	def := loadDiagnosticDefinition(t)

	ctx := aggregatorContext()
	ctx.Inputs["account_id"] = "acct-1"
	ctx.Inputs["instance_id"] = "i-0abc123"
	ctx.Inputs["lookback"] = "1h"
	ctx.Inputs["log_group"] = "/aws/ec2/payments-api"
	ctx.Inputs["log_filter"] = "ERROR"
	ctx.Inputs["dependency_endpoints"] = "db.internal:5432"
	ctx.Tasks["resolve_target"] = completed(`[{"InstanceId":"i-0abc123"}]`)
	ctx.Tasks["lb_target_group_arns"] = completed(`["arn:aws:elasticloadbalancing:tg/payments"]`)
	ctx.Tasks["health"] = completed(`[]`)
	ctx.Vars["LoopItem"] = map[string]any{"arn": "arn:aws:elasticloadbalancing:tg/payments"}

	for _, task := range def.Tasks {
		rendered, err := workflow.ProcessValue(task.Params, ctx)
		if err != nil {
			t.Errorf("task %q failed to render: %v", task.ID, err)
			continue
		}
		encoded, err := json.Marshal(rendered)
		if err != nil {
			t.Errorf("task %q rendered to something unencodable: %v", task.ID, err)
			continue
		}
		if strings.Contains(string(encoded), "{{") {
			t.Errorf("task %q left an unresolved expression: %s", task.ID, encoded)
		}
	}

	// User-supplied values reach the SSM host scripts through env, which the executor
	// single-quote escapes, rather than being interpolated into the script body.
	for _, id := range []string{"host_evidence", "dependency_probes"} {
		task := taskByID(t, def, id)
		env, ok := task.Params["env"].(map[string]any)
		if !ok || len(env) == 0 {
			t.Errorf("%s must pass user input through env, not inline interpolation", id)
			continue
		}
		script, _ := task.Params["script"].(string)
		if strings.Contains(script, "{{") {
			t.Errorf("%s interpolates a template expression directly into the shell script: %q", id, script)
		}
	}
}

// The load balancer fan-out is the one place the template nests tasks inside another
// task's params, where the body renders against LoopItem and the iteration output renders
// against the body's own task ids. Neither is covered by the DAG or aggregator tests.
func TestDiagnosticTemplate_LoadBalancerFanOut(t *testing.T) {
	def := loadDiagnosticDefinition(t)

	// The ARN extractor caps the scan; the cap has to be visible in the result rather
	// than silently applied, so the aggregator reports found vs scanned.
	ctx := workflow.NewTemplateContext(nil, nil)
	groups := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		groups = append(groups, `{"Arn":"arn:aws:elasticloadbalancing:tg/g`+string(rune('a'+i))+`"}`)
	}
	ctx.Tasks["lb_target_groups"] = completed("[" + strings.Join(groups, ",") + "]")

	arnsJSON, ok := runTransform(t, taskByID(t, def, "lb_target_group_arns"), ctx).(string)
	if !ok {
		t.Fatalf("lb_target_group_arns must emit a JSON string for core.foreach items")
	}
	var arns []string
	if err := json.Unmarshal([]byte(arnsJSON), &arns); err != nil {
		t.Fatalf("core.foreach cannot unmarshal items %q: %v", arnsJSON, err)
	}
	if len(arns) != 10 {
		t.Errorf("scanned %d target groups, want the documented cap of 10", len(arns))
	}

	// Body params render against LoopItem, iteration output against the body task id.
	loop := taskByID(t, def, "lb_target_health")
	body, _ := loop.Params["tasks"].([]any)
	if len(body) != 1 {
		t.Fatalf("expected one task in the loop body, got %d", len(body))
	}
	bodyTask, _ := body[0].(map[string]any)
	bodyParams, _ := bodyTask["params"].(map[string]any)

	iterCtx := workflow.NewTemplateContext(nil, nil)
	iterCtx.Inputs["region"] = "us-east-1"
	iterCtx.Inputs["account_id"] = "acct-1"
	iterCtx.Vars["LoopItem"] = map[string]any{"arn": arns[0]}

	rendered, err := workflow.ProcessValue(bodyParams, iterCtx)
	if err != nil {
		t.Fatalf("failed to render the loop body: %v", err)
	}
	command, _ := rendered.(map[string]any)["command"].(string)
	if !strings.Contains(command, "--target-group-arn "+arns[0]) {
		t.Errorf("loop body did not resolve LoopItem: %q", command)
	}

	// A body task that failed under `action: continue` leaves no output, so the
	// iteration output must survive it rather than erroring the whole loop.
	iterCtx.Tasks["health"] = map[string]any{"status": "FAILED", "error": "AccessDenied"}
	if _, err := iterCtx.RenderMap(loop.Params["output"].(map[string]any)); err != nil {
		t.Errorf("iteration output must tolerate a failed body task: %v", err)
	}
}

// aggregatorContext seeds every input the aggregator reads. Callers mutate ctx.Tasks to
// model a specific incident.
func aggregatorContext() *workflow.TemplateContext {
	ctx := workflow.NewTemplateContext(nil, nil)
	ctx.Inputs["region"] = "us-east-1"
	ctx.Inputs["application"] = "payments-api"
	ctx.Inputs["incident_context"] = "INC-1234"
	ctx.Inputs["log_group"] = ""
	ctx.Inputs["dependency_endpoints"] = ""
	ctx.Inputs["collect_host_evidence"] = "true"

	ctx.Tasks["time_window"] = completed(map[string]any{
		"start_iso": "2026-09-02T09:00:00Z", "end_iso": "2026-09-02T10:00:00Z",
		"start_ms": 1, "end_ms": 2, "lookback": "1h",
	})
	ctx.Tasks["select_instance"] = completed(map[string]any{
		"instance_id": "i-0abc123", "name": "payments-api", "state": "running",
		"instance_type": "m5.large", "availability_zone": "us-east-1a",
		"private_ip": "10.0.1.20", "matched_count": 1,
	})
	ctx.Tasks["instance_status"] = completed(`[{"InstanceState":"running","SystemStatus":"ok","InstanceStatus":"ok","Events":null}]`)
	ctx.Tasks["cw_alarms"] = completed(`[]`)
	ctx.Tasks["cw_cpu"] = completed(`{"Datapoints":[{"Average":20.0,"Maximum":31.5}]}`)
	ctx.Tasks["cw_status_check"] = completed(`{"Datapoints":[{"Maximum":0.0,"Sum":0.0}]}`)
	ctx.Tasks["lb_target_groups"] = completed(`[]`)
	ctx.Tasks["lb_target_health"] = map[string]any{"status": "completed", "output": []any{}}
	ctx.Tasks["app_logs"] = map[string]any{"status": "skipped"}
	ctx.Tasks["host_evidence"] = completed(strings.Join([]string{
		"===METRIC:failed_units===", "0",
		"===METRIC:disk_max_pct===", "41",
		"===METRIC:app_service_state===", "active",
		"===SECTION:uptime_load===", " 10:00:00 up 3 days,  load average: 0.20, 0.30, 0.25",
	}, "\n"))
	ctx.Tasks["dependency_probes"] = map[string]any{"status": "skipped"}
	return ctx
}

func completed(payload any) map[string]any {
	return map[string]any{"status": "completed", "output": map[string]any{"data": payload}}
}

func aggregate(t *testing.T, ctx *workflow.TemplateContext) map[string]any {
	t.Helper()
	def := loadDiagnosticDefinition(t)
	out, ok := runTransform(t, taskByID(t, def, "diagnostic_result"), ctx).(map[string]any)
	if !ok {
		t.Fatal("diagnostic_result did not return a structured object")
	}
	return out
}

func findingDetails(t *testing.T, result map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(result["findings"])
	if err != nil {
		t.Fatalf("failed to encode findings: %v", err)
	}
	return string(encoded)
}

func TestDiagnosticAggregator_HealthyTarget(t *testing.T) {
	result := aggregate(t, aggregatorContext())

	if got := result["status"]; got != "healthy" {
		t.Errorf("status = %v, want healthy (findings: %s)", got, findingDetails(t, result))
	}
	target, _ := result["target"].(map[string]any)
	if target["instance_id"] != "i-0abc123" {
		t.Errorf("target instance = %v, want i-0abc123", target["instance_id"])
	}
	if result["incident_context"] != "INC-1234" {
		t.Errorf("incident_context = %v, want INC-1234", result["incident_context"])
	}
	// The optional collectors were not configured, so they must be reported as
	// not_configured / not_applicable rather than counted as missing coverage.
	coverage, _ := result["coverage"].(map[string]any)
	if coverage["unavailable"] != int64(0) && coverage["unavailable"] != 0.0 {
		t.Errorf("unavailable = %v, want 0 for a fully collected run", coverage["unavailable"])
	}
}

func TestDiagnosticAggregator_RanksIncidentSignals(t *testing.T) {
	ctx := aggregatorContext()
	ctx.Inputs["log_group"] = "/aws/ec2/payments-api"
	ctx.Inputs["dependency_endpoints"] = "db.internal:5432"

	ctx.Tasks["instance_status"] = completed(`[{"InstanceState":"running","SystemStatus":"ok","InstanceStatus":"impaired","Events":null}]`)
	ctx.Tasks["cw_alarms"] = completed(`[{"Name":"payments-api-5xx","Metric":"HTTPCode_Target_5XX_Count","Reason":"threshold crossed","Dimensions":[{"Name":"InstanceId","Value":"i-0abc123"}]}]`)
	ctx.Tasks["cw_cpu"] = completed(`{"Datapoints":[{"Average":80.0,"Maximum":97.4}]}`)
	ctx.Tasks["cw_status_check"] = completed(`{"Datapoints":[{"Maximum":1.0,"Sum":3.0}]}`)
	ctx.Tasks["lb_target_groups"] = completed(`[{"Arn":"arn:aws:elasticloadbalancing:tg/payments","Name":"payments","Port":8080}]`)
	ctx.Tasks["lb_target_health"] = map[string]any{"status": "completed", "output": []any{
		map[string]any{
			"arn":     "arn:aws:elasticloadbalancing:tg/payments",
			"targets": `[{"Id":"i-0abc123","Port":8080,"State":"unhealthy","Reason":"Target.FailedHealthChecks","Description":"Health checks failed"}]`,
		},
	}}
	ctx.Tasks["app_logs"] = completed(`[{"Timestamp":1,"Stream":"app","Message":"ERROR connection refused to db.internal:5432"}]`)
	ctx.Tasks["host_evidence"] = completed(strings.Join([]string{
		"===METRIC:failed_units===", "2",
		"===METRIC:disk_max_pct===", "93",
		"===METRIC:app_service_state===", "failed",
		"===SECTION:uptime_load===", " 10:00:00 up 3 days,  load average: 8.10, 7.90, 6.40",
		"===SECTION:disk===", "/dev/root  93% /",
	}, "\n"))
	ctx.Tasks["dependency_probes"] = completed("db.internal:5432 fail tcp_connect_failed_or_timed_out\n")

	result := aggregate(t, ctx)
	details := findingDetails(t, result)

	if got := result["status"]; got != "critical" {
		t.Errorf("status = %v, want critical", got)
	}
	for _, want := range []string{
		"Instance status check is impaired",
		"payments-api-5xx",
		"StatusCheckFailed fired in 3 period(s)",
		"is unhealthy",
		"Dependency db.internal:5432 unreachable",
		`Service \"payments-api\" is failed`,
		"93% full",
		"2 systemd unit(s)",
		"CPU peaked at 97.4%",
	} {
		if !strings.Contains(details, want) {
			t.Errorf("findings do not mention %q\nfindings: %s", want, details)
		}
	}

	// Criticals must outrank warnings so the responder reads the actionable signal first.
	findings, _ := result["findings"].([]any)
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}
	first, _ := findings[0].(map[string]any)
	if first["severity"] != "critical" {
		t.Errorf("first finding severity = %v, want critical", first["severity"])
	}

	evidence, _ := result["evidence"].(map[string]any)
	if regs, _ := evidence["lb_registrations"].([]any); len(regs) != 1 {
		t.Errorf("lb_registrations = %v, want the one matching registration", evidence["lb_registrations"])
	}
	hostMetrics, _ := evidence["host_metrics"].(map[string]any)
	if hostMetrics["disk_max_pct"] != "93" {
		t.Errorf("host_metrics.disk_max_pct = %v, want 93", hostMetrics["disk_max_pct"])
	}
}

// Found on a live run against a demo instance: the app was a bare process, not a systemd
// unit, and the host check reported it as a down service. An application that is not
// packaged as a unit named after itself is ordinary, so it must not raise a critical.
func TestDiagnosticAggregator_MissingSystemdUnitIsNotADownService(t *testing.T) {
	ctx := aggregatorContext()
	ctx.Tasks["host_evidence"] = completed(strings.Join([]string{
		"===METRIC:failed_units===", "0",
		"===METRIC:disk_max_pct===", "28",
		"===METRIC:app_service_state===", "no_such_unit",
		"===SECTION:listening_sockets===", "LISTEN 0 5 0.0.0.0:8080 users:((\"python3\",pid=71412,fd=3))",
	}, "\n"))

	result := aggregate(t, ctx)
	if got := result["status"]; got != "healthy" {
		t.Errorf("status = %v, want healthy (findings: %s)", got, findingDetails(t, result))
	}
	if strings.Contains(findingDetails(t, result), "host_evidence") {
		t.Errorf("a missing systemd unit must not raise a finding: %s", findingDetails(t, result))
	}

	// A unit that exists and is down is still a critical.
	ctx.Tasks["host_evidence"] = completed(strings.Join([]string{
		"===METRIC:failed_units===", "0",
		"===METRIC:disk_max_pct===", "28",
		"===METRIC:app_service_state===", "inactive",
	}, "\n"))
	if got := aggregate(t, ctx)["status"]; got != "critical" {
		t.Errorf("status = %v, want critical for an inactive unit", got)
	}
}

// An endpoint typed without a port is a misconfigured input, not an outage. Reporting it
// as an unreachable dependency would be the same false-critical class as a missing
// systemd unit, so it degrades to a warning and does not mask the endpoints that are real.
func TestDiagnosticAggregator_MalformedEndpointIsNotAnOutage(t *testing.T) {
	ctx := aggregatorContext()
	ctx.Inputs["dependency_endpoints"] = "db.internal,cache.internal:6379"
	ctx.Tasks["dependency_probes"] = completed(strings.Join([]string{
		"db.internal fail invalid_endpoint_format",
		"cache.internal:6379 ok tcp_connect_succeeded",
	}, "\n"))

	result := aggregate(t, ctx)
	if got := result["status"]; got != "degraded" {
		t.Errorf("status = %v, want degraded (findings: %s)", got, findingDetails(t, result))
	}
	details := findingDetails(t, result)
	if !strings.Contains(details, "is not host:port") {
		t.Errorf("expected a malformed-endpoint warning, got: %s", details)
	}
	if strings.Contains(details, "unreachable") {
		t.Errorf("a malformed endpoint must not be reported as unreachable: %s", details)
	}

	// A genuinely unreachable endpoint alongside it is still critical.
	ctx.Tasks["dependency_probes"] = completed(strings.Join([]string{
		"db.internal fail invalid_endpoint_format",
		"cache.internal:6379 fail tcp_connect_failed_or_timed_out",
	}, "\n"))
	if got := aggregate(t, ctx)["status"]; got != "critical" {
		t.Errorf("status = %v, want critical when a well-formed endpoint is down", got)
	}
}

// A collector that failed or was skipped must show up as a coverage gap. Reporting
// "healthy" off signals that were never collected is the failure mode that would make
// this workflow worse than useless during an incident.
func TestDiagnosticAggregator_ReportsUncollectedChecks(t *testing.T) {
	ctx := aggregatorContext()
	ctx.Tasks["instance_status"] = map[string]any{"status": "FAILED", "error": "AccessDenied"}
	ctx.Tasks["cw_alarms"] = map[string]any{"status": "FAILED", "error": "AccessDenied"}

	result := aggregate(t, ctx)

	if got := result["status"]; got != "inconclusive" {
		t.Errorf("status = %v, want inconclusive when collectors failed", got)
	}
	coverage, _ := result["coverage"].(map[string]any)
	if fmtNumber(coverage["unavailable"]) != 2 {
		t.Errorf("unavailable = %v, want 2", coverage["unavailable"])
	}

	encoded, err := json.Marshal(result["checks"])
	if err != nil {
		t.Fatalf("failed to encode checks: %v", err)
	}
	for _, want := range []string{"ec2_status_checks", "cloudwatch_alarms", "unavailable"} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("checks do not mention %q\nchecks: %s", want, encoded)
		}
	}
}

func fmtNumber(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return -1
	}
}
