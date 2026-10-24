package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"nudgebee/llm/common"
)

// A fix that lives in source (#40734): a plan step whose durable change is in code or a deployment source (chart
// values, a Deployment spec) has no command; it states the change in required_change, with its repo when known. A
// person makes the change (or raises it as a pull request) and marks the step done.

// firingPlanRefusal refuses, once, an agent's plan for an alert that is still firing when it offers nothing to do
// now: no actions and only hardening (a change the incident needs is an action), or only command-less fixes and
// no mitigation that buys time. The same plan sent again is stored: the agent then has said why none applies.
// Generic and state-based: it reads the incident's state, not the scenario.
func firingPlanRefusal(ctx context.Context, accountId, eventId string, plan RemediationPlan) error {
	reason := ""
	switch {
	case len(plan.Actions) == 0 && len(plan.OptionalHardening) > 0:
		reason = "the alert is still firing and the plan has no actions: a change the incident needs is an action, with an empty execute_command and a required_change for a change in code or a deployment source, not optional_hardening; and offer a mitigation command that buys time when one exists"
	case len(plan.Actions) > 0 && !planHasCommandStep(plan):
		reason = "the alert is still firing and every step is a change in source, which takes a pull request and a deploy: also offer a mitigation command that buys time now when one exists, kind mitigation, with what it does not fix in trade_off"
	default:
		return nil
	}
	if !alertStillFiring(ctx, accountId, eventId) {
		return nil
	}
	h := sha256.New()
	h.Write([]byte("firing\x00" + eventId + "\x00" + reason))
	for _, a := range plan.Actions {
		h.Write([]byte("\x00" + firstNonEmpty(a.Action, a.Title)))
	}
	for _, item := range plan.OptionalHardening {
		h.Write([]byte("\x00" + item.Target + "\x00" + item.Change))
	}
	if !shownTargetState.firstShowing(hex.EncodeToString(h.Sum(nil)), time.Now()) {
		return nil
	}
	return errors.New(reason + ". If none applies, call propose_fix again with the plan unchanged and say why in the summary")
}

// planHasCommandStep reports whether a step of plan runs a command.
func planHasCommandStep(plan RemediationPlan) bool {
	for _, a := range plan.Actions {
		if strings.TrimSpace(a.ExecuteCommand) != "" {
			return true
		}
	}
	return false
}

// alertStillFiring reports whether the event's incident fires now (alertStateOf). A var so tests can stub it.
var alertStillFiring = func(ctx context.Context, accountId, eventId string) bool {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return false
	}
	readCtx, cancel := context.WithTimeout(ctx, decisionDBTimeout)
	defer cancel()
	inc, err := readIncidentFacts(readCtx, dbManager.Db, accountId, eventId)
	if err != nil || inc == nil {
		return false
	}
	state, _ := alertStateOf(*inc)
	return state == "firing"
}

// sourceWhereLabels name where a required change is made, for the cards and the model.
var sourceWhereLabels = map[string]string{"code": "Code fix", "deployment_source": "Change in the deployment source",
	"infrastructure": "Infrastructure change", "dependency": "Dependency change", "process": "Process change"}

// sourceChangeText describes a step whose fix is a change in source, as the cards and get_remediation_plan show it:
// "Code fix in <target> (<repo>): <change>"; "" for a step with a command or without a usable required change.
func sourceChangeText(a RemediationAction) string {
	rc := a.RequiredChange
	if strings.TrimSpace(a.ExecuteCommand) != "" || !rc.actionable() {
		return ""
	}
	label := firstNonEmpty(sourceWhereLabels[strings.ToLower(strings.TrimSpace(rc.Where))], "Change in source")
	text := label + " in " + strings.TrimSpace(rc.Target)
	if repo := strings.TrimSpace(rc.Repo); repo != "" {
		text += " (" + repo + ")"
	}
	return text + ": " + strings.TrimSpace(rc.Change)
}
