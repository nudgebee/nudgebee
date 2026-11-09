package api

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools"
)

// OutcomeCheck is the server-derived read of the incident's own signal (#40141). Unlike a verify, which
// reads back what one command wrote, it reads the workload's pods or the alarm. Never changes events.status.
type OutcomeCheck struct {
	Command string `json:"command"`
	// Expect is the output that means the alarm is OK; empty means exit code 0 (pods ready) passes.
	Expect string `json:"expect,omitempty"`
	// Describes says what the check observes, for the operator.
	Describes string `json:"describes"`
}

// RemediationSlotOutcome runs the plan's outcome check.
const RemediationSlotOutcome = "outcome"

// outcomeRolloutTimeout stays under the relay's own 120 s execution timeout, so a slow rollout returns
// the rollout's verdict rather than a relay timeout.
const outcomeRolloutTimeout = "90s"

// deriveOutcomeCheck picks the check from the event's source; nil without a reliable read (GCP
// Monitoring has no CLI read of incident state; Cloud Run stays Ready while returning errors).
func deriveOutcomeCheck(ev *eventFacts, provider string) *OutcomeCheck {
	if ev == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "k8s":
		kind, name := rolloutKind(ev.OwnerKind), ev.Owner
		if name == "" {
			kind, name = rolloutKind(ev.SubjectType), ev.SubjectName
		}
		if kind == "" || name == "" || ev.SubjectNS == "" {
			return nil
		}
		cmd, ok := commandFromArgv([]string{"kubectl", "rollout", "status", kind + "/" + name, "-n", ev.SubjectNS, "--timeout=" + outcomeRolloutTimeout})
		if !ok {
			return nil
		}
		return &OutcomeCheck{
			Command:   cmd,
			Describes: fmt.Sprintf("%s %s in %s has all its pods updated and ready", kind, name, ev.SubjectNS),
		}
	case "aws":
		alarm := ev.Labels["aws_event_name"]
		if alarm == "" || !strings.Contains(ev.Labels["aws_event_arn"], ":alarm:") {
			return nil
		}
		region := firstNonEmpty(ev.Labels["aws_region"], ev.Region)
		if region == "" {
			return nil
		}
		// Argv, so an alarm name with spaces is one quoted argument, not several alarm names.
		cmd, ok := commandFromArgv([]string{"aws", "cloudwatch", "describe-alarms", "--alarm-names", alarm, "--region", region, "--query", "MetricAlarms[0].StateValue", "--output", "text"})
		if !ok {
			return nil
		}
		return &OutcomeCheck{
			Command:   cmd,
			Expect:    "OK",
			Describes: fmt.Sprintf("CloudWatch alarm %s is back to OK (an alarm clears only after its evaluation period)", alarm),
		}
	}
	return nil
}

// rolloutKind is kind without its API group (deployment.apps), or "" when kubectl rollout cannot watch it.
func rolloutKind(kind string) string {
	k, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(kind)), ".")
	if revisionedKind[k] {
		return k
	}
	return ""
}

// outcomePassed is three-valued: true when the check passes (pods ready, alarm OK), false when it fails,
// nil when the check could not tell (it did not run, or answered something else).
func outcomePassed(check *OutcomeCheck, response tools.RemediationExecutionResult, exitCodeReported bool) *bool {
	if check.Expect != "" {
		out := cliAnswer(response.Stdout)
		switch {
		case !response.Success || out == "" || out == "None" || out == "INSUFFICIENT_DATA":
			// "None" is the CLI's text output for a query that matched nothing; INSUFFICIENT_DATA is no verdict either.
			return nil
		case out == check.Expect:
			return new(true)
		default:
			return new(false)
		}
	}
	if !exitCodeReported || (!response.Success && rolloutCouldNotRun(response)) {
		return nil
	}
	return new(response.Success)
}

// rolloutCouldNotRun reports a rollout check that failed without reading the rollout: the workload is
// gone or renamed, or the agent may not read it.
func rolloutCouldNotRun(response tools.RemediationExecutionResult) bool {
	out := strings.ToLower(response.Stderr + "\n" + response.Stdout + "\n" + response.Error)
	return strings.Contains(out, "notfound") || strings.Contains(out, "not found") ||
		strings.Contains(out, "(forbidden)") || strings.Contains(out, "is forbidden")
}

// cliAnswer is the last non-empty line a text-output CLI query printed, skipping the cloud workspace's
// "[account: x]" banner.
func cliAnswer(stdout string) string {
	answer := ""
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || (strings.HasPrefix(line, "[account: ") && strings.HasSuffix(line, "]")) {
			continue
		}
		answer = line
	}
	return answer
}

// OutcomeResult is what the panel shows after the check runs, and what is stored on the attempt.
// Command lets the panel ignore a verdict from a check the plan no longer carries.
type OutcomeResult struct {
	Passed  *bool  `json:"passed"`
	Output  string `json:"output"`
	Command string `json:"command"`
	At      string `json:"at"`
}

// recordOutcome attaches the outcome to the event's most recent attempt. A JSONB null data is treated as
// empty: jsonb_set on it returns NULL and would wipe the attempt's data.
func recordOutcome(ctx *security.RequestContext, eventId string, outcome OutcomeResult) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return
	}
	data, err := json.Marshal(outcome)
	if err != nil {
		return
	}
	res, err := dbManager.Db.Exec(`UPDATE event_resolution
		    SET data = jsonb_set(COALESCE(NULLIF(data, 'null'::jsonb), '{}'::jsonb), '{outcome}', $2::jsonb, true), updated_at = NOW()
		  WHERE id = (SELECT id FROM event_resolution WHERE event_id = $1 AND type = 'CommandExecution' ORDER BY created_at DESC LIMIT 1)`,
		eventId, string(data))
	if err != nil {
		ctx.GetLogger().Warn("remediation: failed to record outcome", "error", err)
		return
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		ctx.GetLogger().Info("remediation: no attempt to attach the outcome to", "event_id", eventId)
	}
}

func newOutcomeResult(check *OutcomeCheck, response tools.RemediationExecutionResult, exitCodeReported bool) OutcomeResult {
	return OutcomeResult{
		Passed:  outcomePassed(check, response, exitCodeReported),
		Output:  truncateForRecord(strings.TrimSpace(response.Stdout + "\n" + response.Stderr)),
		Command: check.Command,
		At:      time.Now().UTC().Format(time.RFC3339),
	}
}
