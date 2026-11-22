package cloud

import (
	"fmt"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

func init() {
	playbooks.RegisterAction("cloud_aws_alarm_state", &cloudAWSAlarmStateAction{})
	// Named to sort after cloud_logs: actions run in name order within one
	// enrichment budget, and the instance's own logs matter more.
	playbooks.RegisterAction("cloud_ssm_activity", &cloudAWSSSMActivityAction{})
}

// awsAlarmFiringLookback is how far back earlier firings of the same alarm are
// counted.
const awsAlarmFiringLookback = 7 * 24 * time.Hour

// awsAlarmRecentFiringsShown bounds the firing times listed in the evidence.
const awsAlarmRecentFiringsShown = 5

// awsAlarmStateTimeSources are the event sources whose start is the alarm's own
// state-change time (the collector's poll and pushed state changes). Other
// sources can carry a start synthesised at ingest, which must not be stated as
// when the alarm changed state.
var awsAlarmStateTimeSources = map[string]bool{"AWS_CloudWatch_Alarm": true, "AWS_EventBridge": true}

// eventBudgetExpired reports whether the event's enrichment deadline has
// passed (a zero deadline never expires), so an action with several cloud
// calls can stop between them.
func eventBudgetExpired(event playbooks.PlaybookEvent) bool {
	return !event.Deadline.IsZero() && time.Now().After(event.Deadline)
}

// cloudAWSAlarmStateAction states, for a CloudWatch alarm event, how long the
// alarm has been in its current state and how often it fired before. Both come
// from data already held: the alarm's state-change time is the event's start,
// and earlier firings are earlier events for the same alarm ARN. A standing
// alarm (in ALARM for days) otherwise reads like a fresh failure.
type cloudAWSAlarmStateAction struct{}

func (a *cloudAWSAlarmStateAction) Execute(ctx playbooks.PlaybookActionContext, _ map[string]any) (playbooks.PlaybookActionResponse, error) {
	return a.AutoExecute(ctx)
}

func (a *cloudAWSAlarmStateAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels
	return strings.Contains(labels["aws_event_arn"], ":alarm:") && labels["aws_event_state"] == "ALARM" && ctx.GetAccountId() != ""
}

func (a *cloudAWSAlarmStateAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	event := ctx.GetEvent()
	arn := event.Labels["aws_event_arn"]
	now := time.Now().UTC()

	var earlier []time.Time
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, fmt.Errorf("cloud_aws_alarm_state: database: %w", err)
	}
	// Same predicate as triage's per-alarm history; (account, starts_at) is indexed.
	err = dbms.Db.Select(&earlier,
		`SELECT starts_at FROM events
		 WHERE labels->>'aws_event_arn' = $1 AND cloud_account_id = $2 AND tenant = $3
		   AND starts_at > $4 AND id::text <> $5 AND labels->>'aws_event_state' = 'ALARM'
		 ORDER BY starts_at DESC`,
		arn, ctx.GetAccountId(), ctx.GetTenantId(), now.Add(-awsAlarmFiringLookback), event.EventId)
	if err != nil {
		return nil, fmt.Errorf("cloud_aws_alarm_state: earlier firings: %w", err)
	}

	var stateSince *time.Time
	if awsAlarmStateTimeSources[event.Source] {
		stateSince = event.DetectedAt
	}
	facts := awsAlarmStateFacts(event.Labels["aws_event_state"], stateSince, earlier, now)
	if len(facts) == 0 {
		return nil, nil
	}
	insights := make([]playbooks.PlaybookActionResponseInsight, 0, len(facts))
	for _, f := range facts {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{Message: f, Severity: "info"})
	}
	return formatAzureMarkdownResponse("Alarm State", "### Alarm state\n\n- "+strings.Join(facts, "\n- ")+"\n", insights), nil
}

// awsAlarmStateFacts renders the alarm's state age and its earlier firings as
// plain statements. stateSince is the time the alarm entered its current state
// (the event's own start); earlier is newest first.
func awsAlarmStateFacts(state string, stateSince *time.Time, earlier []time.Time, now time.Time) []string {
	var facts []string
	if stateSince != nil && !stateSince.IsZero() {
		entered := "its current state"
		if state != "" {
			entered = "the " + state + " state"
		}
		facts = append(facts, fmt.Sprintf("The alarm entered %s at %s, %s before this evidence was collected.",
			entered, stateSince.UTC().Format(time.RFC3339), humanDuration(now.Sub(*stateSince))))
	}
	if len(earlier) == 0 {
		facts = append(facts, "No earlier firing of this alarm is recorded in the last 7 days.")
		return facts
	}
	shown := earlier
	if len(shown) > awsAlarmRecentFiringsShown {
		shown = shown[:awsAlarmRecentFiringsShown]
	}
	times := make([]string, len(shown))
	for i, t := range shown {
		times[i] = t.UTC().Format(time.RFC3339)
	}
	facts = append(facts, fmt.Sprintf("Earlier firings of this alarm in the last 7 days: %d (most recent: %s).",
		len(earlier), strings.Join(times, ", ")))
	return facts
}

// humanDuration renders d at the precision a reader needs: minutes under an
// hour, hours under two days, days beyond.
func humanDuration(d time.Duration) string {
	switch {
	case d < 0:
		return "0 minutes"
	case d < time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	}
}

// awsSSMLookback is how far before the alert Run Command history is read.
const awsSSMLookback = 7 * 24 * time.Hour

// awsSSMInvocationsShown bounds the invocations read and listed: enough to see
// a patch run or a script on a schedule, small enough for the evidence cap.
const awsSSMInvocationsShown = 15

// cloudAWSSSMActivityAction states what Systems Manager ran on the alerting
// instance: its last patch operation and the Run Command invocations of the
// week before the alert. Maintenance-window run-command tasks (patching
// included) appear as invocations on the instance, so this shows what actually
// ran, whatever the window targets. A Patch Group tag alone says nothing about
// whether anything ran.
type cloudAWSSSMActivityAction struct{}

func (a *cloudAWSSSMActivityAction) Execute(ctx playbooks.PlaybookActionContext, _ map[string]any) (playbooks.PlaybookActionResponse, error) {
	return a.AutoExecute(ctx)
}

func (a *cloudAWSSSMActivityAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	labels := ctx.GetEvent().Labels
	// An alarm returning to OK needs no explanation of what ran on the host.
	if state := labels["aws_event_state"]; state != "" && state != "ALARM" {
		return false
	}
	return awsInstanceIdRegex.MatchString(labels["aws_event_instance"]) &&
		awsRegionRegex.MatchString(labels["aws_region"]) && ctx.GetAccountId() != ""
}

type awsSSMPatchState struct {
	Operation          string `json:"Operation"`
	OperationStartTime string `json:"OperationStartTime"`
	OperationEndTime   string `json:"OperationEndTime"`
	InstalledCount     int    `json:"InstalledCount"`
	MissingCount       int    `json:"MissingCount"`
	FailedCount        int    `json:"FailedCount"`
	PatchGroup         string `json:"PatchGroup"`
}

type awsSSMInvocation struct {
	DocumentName      string `json:"DocumentName"`
	RequestedDateTime string `json:"RequestedDateTime"`
	Status            string `json:"Status"`
	Comment           string `json:"Comment"`
}

func (a *cloudAWSSSMActivityAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	labels := ctx.GetEvent().Labels
	instance, region := labels["aws_event_instance"], labels["aws_region"]
	if !awsInstanceIdRegex.MatchString(instance) || !awsRegionRegex.MatchString(region) {
		return nil, fmt.Errorf("cloud_aws_ssm_activity: invalid instance or region")
	}
	anchor := time.Now().UTC()
	if t := ctx.GetEvent().AlertStart(); t != nil && !t.IsZero() {
		anchor = t.UTC()
	}
	since := anchor.Add(-awsSSMLookback)
	reqCtx := security.NewRequestContextForTenantAdmin(ctx.GetTenantId(), ctx.GetLogger(), nil, nil)

	if eventBudgetExpired(ctx.GetEvent()) {
		return nil, nil
	}
	var patch struct {
		InstancePatchStates []awsSSMPatchState `json:"InstancePatchStates"`
	}
	if err := runAWSReadCLI(reqCtx, ctx.GetAccountId(), fmt.Sprintf(
		"aws ssm describe-instance-patch-states --instance-ids %s --region %s --output json",
		shellSingleQuote(instance), shellSingleQuote(region)), &patch); err != nil {
		ctx.GetLogger().Warn("cloud_aws_ssm_activity: patch state not read", "instance", instance, "error", err)
		return nil, nil
	}
	if eventBudgetExpired(ctx.GetEvent()) {
		return nil, nil
	}
	var invocations struct {
		CommandInvocations []awsSSMInvocation `json:"CommandInvocations"`
	}
	if err := runAWSReadCLI(reqCtx, ctx.GetAccountId(), fmt.Sprintf(
		"aws ssm list-command-invocations --instance-id %s --filters key=InvokedAfter,value=%s --max-items %d --region %s --output json",
		shellSingleQuote(instance), since.Format(time.RFC3339), awsSSMInvocationsShown, shellSingleQuote(region)), &invocations); err != nil {
		ctx.GetLogger().Warn("cloud_aws_ssm_activity: command invocations not read", "instance", instance, "error", err)
		return nil, nil
	}

	facts, table := awsSSMActivityFacts(patch.InstancePatchStates, invocations.CommandInvocations, since)
	insights := make([]playbooks.PlaybookActionResponseInsight, 0, len(facts))
	for _, f := range facts {
		insights = append(insights, playbooks.PlaybookActionResponseInsight{Message: f, Severity: "info"})
	}
	md := "### Systems Manager activity\n\n- " + strings.Join(facts, "\n- ") + "\n"
	if table != "" {
		md += "\n" + table
	}
	return formatAzureMarkdownResponse("Systems Manager Activity", md, insights), nil
}

// runAWSReadCLI runs one read-only aws command through the collector and
// decodes its JSON output into out. One retry: these are evidence, and the
// enrichment budget matters more than a second attempt.
func runAWSReadCLI(ctx *security.RequestContext, accountId, command string, out any) error {
	resp, err := ExecuteCliWithRetry(ctx, CloudExecuteCliCommandRequest{AccountID: accountId, Command: command}, 1)
	if err != nil {
		return err
	}
	data, ok := resp["data"].(string)
	if !ok {
		return fmt.Errorf("unexpected cli response")
	}
	if strings.TrimSpace(data) == "" {
		return nil
	}
	return common.UnmarshalJson([]byte(data), out)
}

// awsSSMActivityFacts renders the patch state and invocations as plain
// statements plus a short table of the invocations (newest first).
func awsSSMActivityFacts(patch []awsSSMPatchState, invocations []awsSSMInvocation, since time.Time) ([]string, string) {
	var facts []string
	if len(patch) == 0 {
		facts = append(facts, "Systems Manager holds no patch state for this instance.")
	} else {
		p := patch[0]
		fact := fmt.Sprintf("Last patch operation: %s from %s to %s (installed %d, missing %d, failed %d)",
			p.Operation, awsCLITime(p.OperationStartTime), awsCLITime(p.OperationEndTime), p.InstalledCount, p.MissingCount, p.FailedCount)
		if p.PatchGroup != "" {
			fact += fmt.Sprintf(", patch group %q", p.PatchGroup)
		}
		facts = append(facts, fact+".")
	}
	if len(invocations) == 0 {
		facts = append(facts, fmt.Sprintf("No Run Command invocation ran on this instance since %s.", since.Format(time.RFC3339)))
		return facts, ""
	}
	latest := invocations[0]
	count := fmt.Sprintf("%d", len(invocations))
	if len(invocations) >= awsSSMInvocationsShown {
		count = fmt.Sprintf("at least %d", len(invocations))
	}
	facts = append(facts, fmt.Sprintf("Run Command invocations on this instance since %s: %s (most recent: %s at %s, %s).",
		since.Format(time.RFC3339), count, latest.DocumentName, awsCLITime(latest.RequestedDateTime), latest.Status))

	var b strings.Builder
	b.WriteString("| Requested | Document | Status |\n|---|---|---|\n")
	for _, inv := range invocations {
		fmt.Fprintf(&b, "| %s | %s | %s |\n", awsCLITime(inv.RequestedDateTime), inv.DocumentName, inv.Status)
	}
	return facts, b.String()
}

// awsCLITime normalises an aws CLI timestamp to UTC RFC 3339; anything it
// cannot parse is shown as given.
func awsCLITime(s string) string {
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return s
}
