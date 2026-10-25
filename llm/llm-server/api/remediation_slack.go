package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// The alert's plan as Slack and the model read it (#40734): the current plan with each step's decision,
// and each run's follow-up. This file also posts it into the alert's thread; get_remediation_plan
// renders it for the agent.

// slackCardStep is one plan action with its decision.
type slackCardStep struct {
	Index      int
	Title      string
	Kind       string
	Confidence int
	Command    string
	Rollback   string
	TradeOff   string
	// Runnable: NudgeBee runs this step when a person presses Run; otherwise DeliveryReason says why not.
	Runnable       bool
	DeliveryReason string
	// SourceChange describes a step whose fix is a change in source (code, a deployment source, ...): where, what
	// and the repo (sourceChangeText); empty for a command step.
	SourceChange string
	// AfterStep is the 1-based step this one runs after (its After), 0 for none.
	AfterStep int
	// The step's decision, when there is one: its status, how it ended, when, and its status message.
	State            string
	Manual           bool
	ExitCodeReported bool
	At               *time.Time
	Message          string
}

// slackCard is the alert's current plan ("plan"), or one run's follow-up ("followup").
type slackCard struct {
	Kind string
	// plan
	PlanVersion string
	RootCause   string
	Summary     string
	Steps       []slackCardStep
	// followup
	Command       string
	Outcome       string // empty while it waits or runs
	RecheckAt     string
	RecheckReason string
}

// slackCards is one alert's plan and follow-ups.
type slackCards struct {
	TenantId    string      `db:"tenant_id"`
	AccountId   string      `db:"-"`
	EventId     string      `db:"-"`
	Fingerprint string      `db:"fingerprint"`
	Cards       []slackCard `db:"-"`
}

// remediationSlackOn reports whether the account's remediation loop is posted to Slack.
func remediationSlackOn(accountId string) bool {
	return events.RemediationLoopOn(accountId)
}

// decisionRowForCard is one CommandExecution row of the alert as a card reads it.
type decisionRowForCard struct {
	Id            string    `db:"id"`
	Status        string    `db:"status"`
	StatusMessage string    `db:"status_message"`
	UpdatedAt     time.Time `db:"updated_at"`
	Data          []byte    `db:"data"`
}

// decisionCardData is what a decision row's data says that a card shows.
type decisionCardData struct {
	Step             string `json:"step"`
	PlanVersion      string `json:"plan_version"`
	Manual           bool   `json:"manual"`
	ExitCodeReported bool   `json:"exit_code_reported"`
	NotSent          bool   `json:"not_sent"`
	Command          string `json:"command"`
	Followup         *struct {
		Outcome       string `json:"outcome"`
		Summary       string `json:"summary"`
		RecheckAt     string `json:"recheck_at"`
		RecheckReason string `json:"recheck_reason"`
		DoneAt        string `json:"done_at"`
	} `json:"followup"`
}

// loadSlackPlan is the alert's current plan as the panel would get it, or false when there is none to
// show. A var so tests can stub it.
var loadSlackPlan = func(ctx *security.RequestContext, accountId, eventId string) (RemediationPlan, string, bool) {
	stored, ok := loadSavedPlan(ctx, accountId, eventId)
	if !ok || (stored.Stale && !events.RemediationLoopOn(accountId)) {
		return RemediationPlan{}, "", false
	}
	plan := stored.Plan
	assignDelivery(ctx, accountId, &plan)
	return plan, stored.Version, true
}

// buildRemediationSlackCards reads the alert's cards from the database.
func buildRemediationSlackCards(ctx *security.RequestContext, db *sqlx.DB, accountId, eventId string) (slackCards, error) {
	out := slackCards{AccountId: accountId, EventId: eventId}
	if err := db.GetContext(ctx.GetContext(), &out, `SELECT ca.tenant::text AS tenant_id, COALESCE(e.fingerprint, '') AS fingerprint
		  FROM events e JOIN cloud_accounts ca ON ca.id = e.cloud_account_id
		 WHERE e.id = $1 AND e.cloud_account_id = $2`, eventId, accountId); err != nil {
		return out, fmt.Errorf("buildRemediationSlackCards: event: %w", err)
	}
	plan, planVersion, hasPlan := loadSlackPlan(ctx, accountId, eventId)
	var rows []decisionRowForCard
	if err := db.SelectContext(ctx.GetContext(), &rows, `SELECT id, status, COALESCE(status_message, '') AS status_message, updated_at,
		       COALESCE(data, '{}'::jsonb) AS data
		  FROM event_resolution WHERE event_id = $1 AND type = 'CommandExecution' ORDER BY created_at`, eventId); err != nil {
		return out, fmt.Errorf("buildRemediationSlackCards: decisions: %w", err)
	}
	type decided struct {
		row  decisionRowForCard
		data decisionCardData
	}
	decisions := map[string]decided{} // step -> the decision on the current plan that counts
	var followups []slackCard
	for _, row := range rows {
		var d decisionCardData
		_ = json.Unmarshal(row.Data, &d)
		if d.Followup != nil {
			card := slackCard{Kind: "followup", Command: d.Command, Summary: d.Followup.Summary}
			if d.Followup.DoneAt != "" {
				card.Outcome = d.Followup.Outcome
			} else {
				card.RecheckAt, card.RecheckReason = d.Followup.RecheckAt, d.Followup.RecheckReason
			}
			followups = append(followups, card)
		}
		// Rows arrive oldest first, so a later decision on the same step replaces an earlier one (a run
		// that never ran, or one with an unknown outcome a person ran again).
		if hasPlan && d.PlanVersion == planVersion && d.Step != "" {
			decisions[d.Step] = decided{row, d}
		}
	}
	if hasPlan {
		card := slackCard{Kind: "plan", PlanVersion: planVersion, RootCause: plan.RootCause, Summary: plan.Summary}
		for i, a := range plan.Actions {
			step := slackCardStep{Index: i, Title: firstNonEmpty(a.Title, a.Action), Kind: a.Kind,
				Confidence: int(a.Confidence), Command: strings.TrimSpace(a.ExecuteCommand), Rollback: strings.TrimSpace(a.RollbackCommand),
				TradeOff: a.TradeOff, DeliveryReason: a.DeliveryReason, SourceChange: sourceChangeText(a)}
			step.Runnable = a.Delivery == DeliveryRun && step.Command != ""
			if d, ok := decisions[fmt.Sprintf("%d:apply", i)]; ok {
				at := d.row.UpdatedAt
				step.State, step.Manual, step.ExitCodeReported, step.At, step.Message =
					d.row.Status, d.data.Manual, d.data.ExitCodeReported, &at, d.row.StatusMessage
			}
			card.Steps = append(card.Steps, step)
		}
		linkAfterSteps(plan, card.Steps)
		out.Cards = append(out.Cards, card)
	}
	out.Cards = append(out.Cards, followups...)
	return out, nil
}

// planAfterInvestigation asks Nubi for the alert's plan once its investigation completes, with the loop on: a
// planning turn in the alert's conversation writes it in the background (remediation_planning.go), and storing
// it posts it here. The one-shot planner no longer runs after an investigation.
func planAfterInvestigation(ctx *security.RequestContext, accountId, eventId string) {
	if strings.TrimSpace(eventId) == "" {
		return
	}
	if _, refusal, err := requestPlanAfterInvestigation(ctx, accountId, eventId, planRequestInput{Kind: planTurnAuto}); err != nil || refusal != "" {
		ctx.GetLogger().Info("remediation_planning: no plan after the investigation", "account_id", accountId, "event_id", eventId, "reason", refusal, "error", err)
	}
}

// requestPlanAfterInvestigation is requestBackgroundPlan; a var so tests can capture it.
var requestPlanAfterInvestigation = requestBackgroundPlan

// planBuilds: one plan build per alert at a time per process; a waiter then finds the plan current.
// ponytail: per process; two pods building the same alert together can still build twice (the newer plan wins).
var planBuilds = &planBuildGuard{busy: map[string]chan struct{}{}}

type planBuildGuard struct {
	mu sync.Mutex
	// busy holds a channel per alert being built, closed when the build ends.
	busy map[string]chan struct{}
}

// claim takes the alert's slot, after the build in flight on this process, if any, ends (waited). End it with
// release.
func (g *planBuildGuard) claim(ctx context.Context, eventId string) (waited bool, err error) {
	for {
		g.mu.Lock()
		done, busy := g.busy[eventId]
		if !busy {
			g.busy[eventId] = make(chan struct{})
			g.mu.Unlock()
			return waited, nil
		}
		g.mu.Unlock()
		waited = true
		select {
		case <-done:
		case <-ctx.Done():
			return waited, fmt.Errorf("waiting for the plan being built: %w", ctx.Err())
		}
	}
}

// release ends a claim.
func (g *planBuildGuard) release(eventId string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if done, busy := g.busy[eventId]; busy {
		close(done)
		delete(g.busy, eventId)
	}
}

// slackSource is the decision source a Slack click names.
const slackSource = "slack"

// linkAfterSteps sets each step's AfterStep from its After (an action's name).
func linkAfterSteps(plan RemediationPlan, steps []slackCardStep) {
	for i, p := range planRunsAfter(plan) {
		if p >= 0 && i < len(steps) && p < len(steps) {
			steps[i].AfterStep = p + 1
		}
	}
}

// The transport to the alert's Slack thread (#40734): notifications-server's /api/remediation/thread. A send is
// messages for one alert thread; each keyed message is posted once and, with Edit, edited in place by later sends
// (remediation_thread_sync.go draws them from the alert's state). StateSeq orders sends: notifications-server
// refuses one older than the state a message shows, and answers 409 (busy) while another send for the thread is
// being posted, which is retried here, never taken as delivered.

// threadButton is a button notifications-server renders: pick (Use this fix), pr (Raise a PR), manual (I made this
// change), other (Other fixes…), resolve, or link.
type threadButton struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Option int    `json:"option,omitempty"`
	RunId  string `json:"run_id,omitempty"`
	Style  string `json:"style,omitempty"`
	Url    string `json:"url,omitempty"`
}

// threadPost is one message for the alert's thread. Key names it in the thread; Token goes in its metadata and
// names what its buttons act on (remediation-plan:… or remediation-run:…); it proves nothing, every click is decided
// by the server.
type threadPost struct {
	Text  string `json:"text"`
	Token string `json:"token,omitempty"`
	Key   string `json:"key,omitempty"`
	Edit  bool   `json:"edit,omitempty"`
	// OnlyIfPosted: edited when it was posted, never posted (the replaced plan's options).
	OnlyIfPosted bool           `json:"only_if_posted,omitempty"`
	Buttons      []threadButton `json:"buttons,omitempty"`
	Status       string         `json:"status,omitempty"`
}

// threadPosts is one send: messages for one alert's thread (Fingerprint's), in order. A send for an alert with no
// thread yet waits for it.
type threadPosts struct {
	TenantId    string       `json:"tenant_id"`
	AccountId   string       `json:"account_id"`
	EventId     string       `json:"event_id"`
	Fingerprint string       `json:"fingerprint"`
	Posts       []threadPost `json:"posts"`
	// StateSeq is the number of the alert state the send was drawn from (remediation_thread_state_seq), 0 for a
	// one-off report.
	StateSeq int64 `json:"state_seq,omitempty"`
	// MainFingerprint is the episode's main alert when this thread is a duplicate's.
	MainFingerprint string `json:"main_fingerprint,omitempty"`
}

// Slack sends: how many times and how long between them, and the request's own deadline. A busy thread is tried
// again, backing off from slackBusyBackoff up to slackBusyMaxBackoff, for slackBusyFor: longer than
// notifications-server holds a thread's claim (60 s) plus a rate-limited post, so the newest state outlasts the
// send ahead of it (which also draws a state refused while it held the claim). Vars so tests can shorten them.
var (
	slackSendAttempts   = 3
	slackSendBackoff    = 2 * time.Second
	slackSendTimeout    = 15 * time.Second
	slackBusyFor        = 90 * time.Second
	slackBusyBackoff    = 500 * time.Millisecond
	slackBusyMaxBackoff = 8 * time.Second
)

// postToAlertThread sends posts to notifications-server. A var so tests can capture it.
var postToAlertThread = func(ctx context.Context, posts threadPosts) (int, error) {
	// Headers first: HttpWithHeaders replaces the header map, so after the body it would drop the
	// Content-Type HttpWithJsonBody sets, and notifications-server would refuse the body.
	var options []common.HttpOption
	if config.Config.NotificationServerToken != "" {
		options = append(options, common.HttpWithHeaders(map[string]string{"X-ACTION-TOKEN": config.Config.NotificationServerToken}))
	}
	options = append(options, common.HttpWithJsonBody(posts), common.HttpWithContext(ctx))
	resp, err := common.HttpPost(config.Config.NotificationServerUrl+"/api/remediation/thread", options...)
	if err != nil {
		return 0, fmt.Errorf("postToAlertThread: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

// threadPostsInFlight counts the thread sends running in the background, so tests can wait for them to end
// before they change what the sends call.
var threadPostsInFlight sync.WaitGroup

// sendToAlertThread sends posts, retrying a failure that may pass and a busy thread (409: another send for it is
// being posted). A busy answer is never taken as delivered: the newer state would be lost.
// sleepCtx waits d, or until ctx is done; it reports whether the wait ran to the end.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func sendToAlertThread(ctx context.Context, logger interface{ Warn(string, ...any) }, posts threadPosts) {
	failures, busy := 0, 0
	var busySince time.Time
	backoff := slackBusyBackoff
	for {
		sendCtx, cancel := context.WithTimeout(ctx, slackSendTimeout)
		status, err := postToAlertThread(sendCtx, posts)
		cancel()
		switch {
		case err == nil && status == http.StatusConflict:
			busy++
			if busySince.IsZero() {
				busySince = time.Now()
			}
			if time.Since(busySince)+backoff > slackBusyFor {
				logger.Warn("remediation_slack: thread stayed busy", "attempts", busy, "state_seq", posts.StateSeq)
				return
			}
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = min(2*backoff, slackBusyMaxBackoff)
			continue
		case err == nil && status < 500:
			if status >= 300 {
				logger.Warn("remediation_slack: posts refused", "status", status)
			}
			return
		}
		failures++
		logger.Warn("remediation_slack: send failed", "attempt", failures, "status", status, "error", err)
		if failures >= slackSendAttempts {
			return
		}
		if !sleepCtx(ctx, slackSendBackoff*time.Duration(failures*failures)) {
			return
		}
	}
}

// postFollowupToThread posts a follow-up check in the alert's threads (the episode's), once per turn: the agent's
// report, or why there was none, then draws the threads again. A superseded check is not posted: the newer run's
// check is. "Likely fixed" is not posted as a report: it is the verdict message, with Resolve, that the sync draws.
func postFollowupToThread(run followupRun, end map[string]any, recheckAt string) {
	ctx := security.NewRequestContextForSuperAdmin()
	if outcome, _ := end["outcome"].(string); outcome == followupLikelyFixed {
		syncAlertThread(ctx, run.AccountId, run.EventId)
		return
	}
	text := followupPostText(run, end, recheckAt)
	if text == "" {
		syncAlertThread(ctx, run.AccountId, run.EventId)
		return
	}
	report := threadPost{Text: text, Key: "followup:" + run.Id + ":" + run.MessageId}
	inAlertThreadsBackground(ctx, run.AccountId, run.EventId, func(view alertThreadView) []threadPost {
		return []threadPost{report}
	}, false)
	syncAlertThread(ctx, run.AccountId, run.EventId)
}

// followupPostText is a settled follow-up check as a thread message, in markdown; "" for one not posted.
func followupPostText(run followupRun, end map[string]any, recheckAt string) string {
	outcome, _ := end["outcome"].(string)
	reason, _ := end["reason"].(string)
	body, _ := end["summary"].(string)
	switch outcome {
	case followupSuperseded:
		return ""
	case followupStopped:
		body = "**Status:** a person stopped this check."
	case followupNeedsHuman, followupFailed:
		why := map[string]string{
			"attempt_limit":     fmt.Sprintf("%d plans of this alert were already checked in 24 hours", config.Config.RemediationFollowupMaxRuns),
			"conversation_busy": "the alert's conversation stayed busy for 2 hours",
			"turn_stuck":        "the check did not finish within 2 hours",
			"budget":            "the AI budget is used up",
		}[reason]
		if why != "" {
			why = ": " + why
		}
		body = "**Status:** not checked" + why + ".\n**Next:** a person decides what to try next."
	}
	if strings.TrimSpace(body) == "" {
		body = "**Status:** the check finished without a report."
	}
	if at, err := time.Parse(time.RFC3339Nano, recheckAt); err == nil {
		body += fmt.Sprintf("\n**Re-check:** NudgeBee looks again at %s UTC.", at.UTC().Format("15:04"))
	}
	var data struct {
		Step string `json:"step"`
	}
	_ = json.Unmarshal(run.Data, &data)
	after := "after a run"
	var index int
	if _, err := fmt.Sscanf(data.Step, "%d:", &index); err == nil {
		after = fmt.Sprintf("after step %d", index+1)
		if strings.HasSuffix(data.Step, ":"+RemediationSlotRollback) {
			after = fmt.Sprintf("after undoing step %d", index+1)
		}
	}
	return "### Follow-up check, " + after + "\n" + body
}
