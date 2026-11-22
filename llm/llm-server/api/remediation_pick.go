package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// A pick of one of the alert's fix options (#40734, remediation loop): "Use this fix" or "Raise a PR" on the
// Remediation tab (and, later, in the alert's Slack thread) is a message into the alert's conversation, as the person
// who picked. It is sent as a structured pick (remediation_pick on /v1/completions/chat) so the server, not the
// client, decides whether it may be sent: the person can write to the account (only they can answer the approval it
// leads to, and a pending approval holds the alert's re-investigation back), the fixes are the ones they saw, the
// option exists and is carried out that way, and nothing is under way on the alert (no approval waiting, no step
// running or being checked). The message text is the server's, so every surface sends the same one.

// remediationPickRequest is the structured pick a chat request carries.
type remediationPickRequest struct {
	// EventId is the event whose fixes were shown: the plan's event (a duplicate's main event). Empty: the request's
	// config.event_id.
	EventId string `json:"event_id"`
	// PlanVersion is the plan_version of the fixes the person saw.
	PlanVersion string `json:"plan_version"`
	// Option is the option picked, from 1.
	Option int `json:"option"`
	// Kind is how it is carried out: run (Nubi runs it after an approval) or pr (a change raised as a pull request).
	Kind string `json:"kind"`
	// Repo is the repository a person picked for a pr: one of the account's connected repositories. Needed when the
	// option's repository is not known for the workload (optionRepos); ignored when it is.
	Repo string `json:"repo,omitempty"`
}

// Pick kinds.
const (
	pickKindRun = "run"
	pickKindPR  = "pr"
)

// pickFacts is what a pick is checked against, read from the server's records.
type pickFacts struct {
	// PlanEventId is the event whose plan the pick acts on; Session that alert's conversation (event-<fingerprint>).
	PlanEventId string
	Session     string
	Plan        storedPlan
	HasPlan     bool
	// RunInFlight: a step of the alert is running or its follow-up check has not finished.
	RunInFlight bool
	// WaitingSince is when the newest approval still waiting in the alert's conversation was asked; nil for none.
	WaitingSince *time.Time
	// Repos says how each option that changes a repository is raised; PickedRepo is the connected repository the
	// person's pick names ("" for none, or one that is not connected).
	Repos      []optionRepo
	PickedRepo string
}

// readPickFacts reads the facts for a pick of eventId's fixes on accountId. found is false when the event is not one
// of the account's. A var so tests can stub it.
var readPickFacts = func(ctx *security.RequestContext, accountId, eventId string) (facts pickFacts, found bool, err error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return facts, false, fmt.Errorf("readPickFacts: %w", err)
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	info, err := repo.GetEventInfo(ctx, eventId, accountId)
	if err != nil {
		return facts, false, fmt.Errorf("readPickFacts: event: %w", err)
	}
	if info == nil {
		return facts, false, nil
	}
	planEvent, planInfo := planEventAndInfo(ctx, repo, accountId, eventId, info)
	alert := firstNonEmpty(planInfo.Fingerprint, planEvent)
	facts.PlanEventId, facts.Session = planEvent, events.SessionIdPrefixEvent+alert
	facts.Plan, facts.HasPlan = loadSavedPlan(ctx, accountId, planEvent)
	if facts.HasPlan {
		facts.Repos, _ = planOptionRepos(ctx, accountId, planEvent, facts.Plan.Plan)
	}
	readCtx, cancel := context.WithTimeout(requestGoContext(ctx), decisionDBTimeout)
	defer cancel()
	if facts.RunInFlight, err = events.AlertRunInFlight(readCtx, dbManager.Db, accountId, alert); err != nil {
		return facts, true, fmt.Errorf("readPickFacts: runs: %w", err)
	}
	waiting, err := waitingConfirmationsIn(ctx, dbManager.Db, accountId, facts.Session, planEvent)
	if err != nil {
		return facts, true, fmt.Errorf("readPickFacts: approvals: %w", err)
	}
	if len(waiting) > 0 {
		at := waiting[0].CreatedAt
		facts.WaitingSince = &at
	}
	return facts, true, nil
}

// pickRefusal is the HTTP status and sentence refusing a pick, or 0 when it may be sent. sessionId is the session
// the request names ("" for none: the alert's own is used).
func pickRefusal(pick remediationPickRequest, sessionId string, facts pickFacts) (int, string) {
	switch {
	case !facts.HasPlan:
		return http.StatusConflict, "remediation: this alert has no fixes to pick from yet."
	case strings.TrimSpace(pick.PlanVersion) == "" || strings.TrimSpace(pick.PlanVersion) != facts.Plan.Version:
		return http.StatusConflict, "remediation: these fixes were replaced since you saw them. Look at the newest fixes and pick again."
	case sessionId != "" && sessionId != facts.Session:
		return http.StatusBadRequest, "remediation: a pick goes into the alert's own conversation"
	case pick.Option < 1 || pick.Option > len(facts.Plan.Plan.Actions):
		return http.StatusBadRequest, fmt.Sprintf("remediation: there is no option %d in these fixes", pick.Option)
	}
	action := facts.Plan.Plan.Actions[pick.Option-1]
	switch pick.Kind {
	case pickKindRun:
		if pickMode(action) != pickKindRun {
			return http.StatusConflict, fmt.Sprintf("remediation: option %d is a change made outside NudgeBee, not one NudgeBee runs", pick.Option)
		}
	case pickKindPR:
		if pickMode(action) != pickKindPR {
			return http.StatusConflict, fmt.Sprintf("remediation: option %d is not a change in a repository, so no pull request can be raised for it", pick.Option)
		}
		if prRepoOf(pick, facts) == "" {
			if strings.TrimSpace(pick.Repo) != "" {
				return http.StatusBadRequest, "remediation: that repository is not one of this account's connected repositories"
			}
			return http.StatusConflict, fmt.Sprintf("remediation: pick the repository to raise option %d's pull request in", pick.Option)
		}
	default:
		return http.StatusBadRequest, "remediation: a pick is run or pr"
	}
	if facts.WaitingSince != nil {
		return http.StatusConflict, fmt.Sprintf("remediation: a fix of this alert is already waiting for approval (asked at %s UTC). Answer it first.",
			facts.WaitingSince.UTC().Format("15:04"))
	}
	if facts.RunInFlight {
		return http.StatusConflict, "remediation: a step of this alert is running or its check has not finished. Pick again when it reports."
	}
	return 0, ""
}

// prWheres are the kinds of change made in a repository, raised as a pull request.
var prWheres = map[string]bool{"code": true, "deployment_source": true, "infrastructure": true, "dependency": true}

// pickMode is how an option is carried out, as the tab shows it (remediationLoop.js optionMode): pr for a change in a
// repository (its repository known, confirmed or picked: optionRepos), manual for any other change a person makes,
// run otherwise.
func pickMode(a RemediationAction) string {
	rc := a.RequiredChange
	if rc == nil || strings.TrimSpace(rc.Change) == "" {
		return pickKindRun
	}
	if repoChange(a) {
		return pickKindPR
	}
	return "manual"
}

// prRepoOf is the repository a pr pick is raised in: the option's when the workload is known to come from it, else
// the connected repository the person picked; "" when neither.
func prRepoOf(pick remediationPickRequest, facts pickFacts) string {
	if r := optionRepoOf(facts.Repos, pick.Option); r.Known == repoKnownWorkload {
		return r.Repo
	}
	if strings.TrimSpace(pick.Repo) != "" {
		return facts.PickedRepo
	}
	return ""
}

// prReplyAsk ends a Raise a PR pick: seen on dev, the reply opened with the whole diff and the PR's link was its last
// line.
const prReplyAsk = "Reply with the pull request's link first and one line on what it changes; the diff is in the PR."

// pickQueryText is the message a pick sends into the alert's conversation: the same words on every surface.
func pickQueryText(pick remediationPickRequest, plan RemediationPlan, repo string) string {
	a := plan.Actions[pick.Option-1]
	if pick.Kind == pickKindPR {
		rc := a.RequiredChange
		text := fmt.Sprintf("Raise a PR for option %d", pick.Option)
		if plan.Version > 0 {
			text += fmt.Sprintf(" of plan v%d", plan.Version)
		}
		text += ": " + strings.TrimSpace(rc.Change)
		if t := strings.TrimSpace(rc.Target); t != "" {
			text += " in " + t
		}
		text += " (repo " + strings.TrimSpace(repo) + ")."
		if cause := firstSentence(plan.RootCause); cause != "" {
			text += " Root cause: " + cause
		}
		return text + " " + prReplyAsk
	}
	return fmt.Sprintf("Go with option %d: %s.", pick.Option, strings.TrimRight(strings.TrimSpace(firstNonEmpty(a.Action, a.Title)), "."))
}

// firstSentence is the first sentence (or line) of text, ending in its stop; "" for none.
func firstSentence(text string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0])
	if line == "" {
		return ""
	}
	for i, r := range line {
		if (r == '.' || r == '!' || r == '?') && (i+1 == len(line) || line[i+1] == ' ') {
			return line[:i+1]
		}
	}
	return line + "."
}

// errPickNeedsWrite is the refusal a person without write access gets.
var errPickNeedsWrite = errors.New("remediation: write access to this account is required to pick a fix")

// alertConversationIdOf is the id of the alert's conversation (session) on account, or "" when it has none yet. A var
// so tests can stub it.
var alertConversationIdOf = func(accountId, session string) (string, error) {
	conv, err := core.GetConversationDao().GetConversationBySession(accountId, session)
	if err != nil {
		return "", err
	}
	if conv.ID == uuid.Nil {
		return "", nil
	}
	return conv.ID.String(), nil
}

// applyRemediationPick checks a structured pick and, when it may be sent, rewrites the request into the alert's
// message: the server's text, the alert's conversation and the plan's event. It answers the refusal itself and
// reports false then; true lets the request go on (also when it carries no pick). Called by the chat handler after
// the account read check.
func applyRemediationPick(c *gin.Context, ctx *security.RequestContext, request *ConversationApiRequest) bool {
	pick := request.RemediationPick
	if pick == nil {
		return true
	}
	fail := func(code int, message string) bool {
		ctx.GetLogger().Info("remediation: pick refused", "status", code, "reason", message, "option", pick.Option, "kind", pick.Kind)
		c.JSON(code, BuildApiResponse(nil, []error{common.Error{Message: message}}))
		return false
	}
	if strings.TrimSpace(request.AgentId) != "" || strings.TrimSpace(request.Resolution) != "" {
		return fail(http.StatusBadRequest, "remediation: a pick is a new message, not an answer")
	}
	pick.Kind = strings.ToLower(strings.TrimSpace(pick.Kind))
	on, err := events.RemediationLoopRead(request.AccountId)
	if err != nil {
		ctx.GetLogger().Warn("remediation: pick: loop flag not read", "error", err)
		return fail(http.StatusServiceUnavailable, "remediation: could not check this account's settings; try again")
	}
	if !on {
		return fail(http.StatusBadRequest, "remediation: picking a fix needs the remediation loop, which is off for this account")
	}
	if !canWriteAlert(ctx.GetSecurityContext(), request.AccountId) {
		return fail(http.StatusForbidden, errPickNeedsWrite.Error())
	}
	eventId := strings.TrimSpace(firstNonEmpty(pick.EventId, request.Config.EventId))
	if eventId == "" {
		return fail(http.StatusBadRequest, "remediation: a pick names the event whose fixes were shown")
	}
	facts, found, err := readPickFacts(ctx, request.AccountId, eventId)
	if err != nil {
		ctx.GetLogger().Error("remediation: pick: facts not read", "error", err, "event_id", eventId)
		return fail(http.StatusServiceUnavailable, "remediation: could not read this alert's fixes; try again")
	}
	if !found {
		return fail(http.StatusNotFound, "remediation: event not found")
	}
	if pick.Kind == pickKindPR && strings.TrimSpace(pick.Repo) != "" {
		facts.PickedRepo = integrationRepo(ctx, request.AccountId, pick.Repo)
	}
	if code, message := pickRefusal(*pick, strings.TrimSpace(request.SessionId), facts); code != 0 {
		return fail(code, message)
	}
	// The pick goes into the alert's own conversation: a conversation id that names any other is refused.
	if id := strings.TrimSpace(request.ConversationId); id != "" {
		alertConv, err := alertConversationIdOf(request.AccountId, facts.Session)
		if err != nil {
			ctx.GetLogger().Warn("remediation: pick: alert conversation not read", "error", err)
			return fail(http.StatusServiceUnavailable, "remediation: could not read this alert's conversation; try again")
		}
		if alertConv != "" && !strings.EqualFold(alertConv, id) {
			return fail(http.StatusBadRequest, "remediation: a pick goes to the alert's own conversation")
		}
	}
	request.Query = pickQueryText(*pick, facts.Plan.Plan, prRepoOf(*pick, facts))
	request.SessionId = facts.Session
	request.Config.EventId = facts.PlanEventId
	ctx.GetLogger().Info("remediation: pick accepted", "event_id", facts.PlanEventId, "option", pick.Option, "kind", pick.Kind, "plan_version", facts.Plan.Version)
	// The alert's threads stop offering a pick while the conversation works on this one.
	syncAlertThread(ctx, request.AccountId, facts.PlanEventId)
	return true
}
