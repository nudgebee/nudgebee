package api

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// The alert's Slack thread as a second view of the server's state (#40734, remediation loop). syncAlertThread draws
// it from the records the Remediation tab reads — the stored plan, the loop state (approvals waiting, the plan's
// relevance, a re-fire), the runs and the event's status — and sends it as keyed messages:
//
//   - options:<plan event>:<plan version>: the fix options, edited in place, with a status footer. Its buttons (Use
//     this fix, Raise a PR, I made this change, Other fixes…) show only while a person can pick, as on the tab.
//   - verdict:<run id>: a run the verdict code judged likely fixed (or cleared before the change), with Resolve while
//     it can still be confirmed.
//
// The older plan's options message is edited to say it was replaced. Everything else (picked, waiting for approval,
// running, verifying until HH:MM, came back, resolved) is a footer line: an edit, which notifies nobody. Only an
// approval card (posted by the conversation's follow-up) and a verdict are new posts.
//
// Every hook just calls syncAlertThread; a sync is idempotent, so a missed or reordered one is fixed by the next.
// Order: each sync takes the next remediation_thread_state_seq before it reads the state, so the highest number
// has read every change whose hook ran before it took its number, and notifications-server refuses a lower one.
// Duplicates: the same state goes into every live thread of the episode, the main alert's and each DUPLICATE or
// SUPPRESSED follower's (its own fingerprint), keyed per thread.

// alertThreadRun is one run of the alert (a CommandExecution or PullRequest row of the plan event), as the thread
// reads it.
type alertThreadRun struct {
	Id          string    `db:"id"`
	Type        string    `db:"type"`
	Status      string    `db:"status"`
	ResolverId  string    `db:"resolver_id"`
	PrLifecycle string    `db:"pr_lifecycle_state"`
	CreatedAt   time.Time `db:"created_at"`
	RawData     []byte    `db:"data"`
	Data        alertThreadRunData
}

// alertThreadRunData is what a run's data says that the thread shows.
type alertThreadRunData struct {
	Option       int    `json:"option"`
	Step         string `json:"step"`
	PlanVersion  string `json:"plan_version"`
	Slot         string `json:"slot"`
	Manual       bool   `json:"manual"`
	NotSent      bool   `json:"not_sent"`
	ApprovedBy   string `json:"approved_by"`
	ApprovedFrom string `json:"approved_from"`
	ApprovalNote string `json:"approval_note"`
	Source       string `json:"source"`
	Followup     *struct {
		Verdict       string `json:"verdict"`
		VerdictReason string `json:"verdict_reason"`
		VerdictAt     string `json:"verdict_at"`
		HoldUntil     string `json:"hold_until"`
		RecheckAt     string `json:"recheck_at"`
		DoneAt        string `json:"done_at"`
		Outcome       string `json:"outcome"`
		Reason        string `json:"reason"`
	} `json:"followup"`
}

// option is the plan option (from 1) the run carried out: data.option, or, for a record that names only its step
// (a person's "I made this change" is <0-based step>:apply), the step's; 0 for none.
func (d alertThreadRunData) option() int {
	if d.Option > 0 {
		return d.Option
	}
	var index int
	if _, err := fmt.Sscanf(d.Step, "%d:", &index); err == nil && index >= 0 {
		return index + 1
	}
	return 0
}

// alertResolvedBy is the plan event's nb_resolved_by label.
type alertResolvedBy struct {
	RunId           string `json:"run_id"`
	ConfirmedBy     string `json:"confirmed_by"`
	ConfirmedByName string `json:"confirmed_by_name"`
}

// alertThreadView is everything one sync draws, read once.
type alertThreadView struct {
	TenantId    string
	AccountId   string
	PlanEventId string
	// MainFingerprint is the plan event's alert (its fingerprint, or its id without one); Threads the fingerprints
	// whose threads show it, the main alert's first.
	MainFingerprint string
	Threads         []string
	Seq             int64
	HasPlan         bool
	PlanVersion     string
	Plan            RemediationPlan
	Loop            *remediationLoopState
	Runs            []alertThreadRun // oldest first
	Names           map[string]string
	NbStatus        string
	ResolvedAt      *time.Time
	ResolvedBy      alertResolvedBy
}

// remediationThreadSeqSQL is the next number of the alert-thread state order (migration V1068).
const remediationThreadSeqSQL = `SELECT nextval('remediation_thread_state_seq')`

// nextThreadStateSeq takes the next state number. A var so tests can stub it.
var nextThreadStateSeq = func(ctx context.Context, db *sqlx.DB) (int64, error) {
	var seq int64
	if err := db.GetContext(ctx, &seq, remediationThreadSeqSQL); err != nil {
		return 0, fmt.Errorf("nextThreadStateSeq: %w", err)
	}
	return seq, nil
}

// episodeFollowerThreads are the fingerprints of the plan event's live followers whose alert is another one (marked
// its duplicates, labels.nb_duplicate_of, DUPLICATE or SUPPRESSED), newest first, at most 10. Followers with the main
// alert's own fingerprint share its thread. A var so tests can stub it.
var episodeFollowerThreads = func(ctx context.Context, db *sqlx.DB, accountId, planEventId string) ([]string, error) {
	var out []string
	err := db.SelectContext(ctx, &out, `SELECT fingerprint FROM (
		SELECT DISTINCT ON (e.fingerprint) e.fingerprint, e.created_at FROM events e JOIN events p ON p.id = $2::uuid
		 WHERE e.cloud_account_id = $1::uuid AND p.cloud_account_id = $1::uuid AND e.id <> p.id
		   AND COALESCE(e.nb_status, '') IN ('DUPLICATE', 'SUPPRESSED') AND e.created_at >= p.created_at
		   AND NULLIF(e.fingerprint, '') IS NOT NULL AND e.fingerprint <> COALESCE(p.fingerprint, '')
		   AND `+duplicatesOf("$2")+`
		 ORDER BY e.fingerprint, e.created_at DESC) f
		 ORDER BY created_at DESC LIMIT 10`, accountId, planEventId)
	if err != nil {
		return nil, fmt.Errorf("episodeFollowerThreads: %w", err)
	}
	return out, nil
}

// readThreadRuns reads the plan event's runs, oldest first. A var so tests can stub it.
var readThreadRuns = func(ctx context.Context, db *sqlx.DB, planEventId string) ([]alertThreadRun, error) {
	var rows []alertThreadRun
	if err := db.SelectContext(ctx, &rows, `SELECT id::text AS id, type, status, COALESCE(resolver_id, '') AS resolver_id,
		       COALESCE(pr_lifecycle_state, '') AS pr_lifecycle_state, created_at, COALESCE(data, '{}'::jsonb) AS data
		  FROM event_resolution WHERE event_id = $1::uuid AND type IN ('CommandExecution', 'PullRequest')
		 ORDER BY created_at`, planEventId); err != nil {
		return nil, fmt.Errorf("readThreadRuns: %w", err)
	}
	for i := range rows {
		_ = json.Unmarshal(rows[i].RawData, &rows[i].Data)
	}
	return rows, nil
}

// alertEventState is the plan event's tenant, triage status and resolve label.
type alertEventState struct {
	TenantId   string     `db:"tenant_id"`
	NbStatus   string     `db:"nb_status"`
	ChangedAt  *time.Time `db:"nb_status_changed_at"`
	ResolvedBy string     `db:"resolved_by"`
}

// readAlertEventState reads it. A var so tests can stub it.
var readAlertEventState = func(ctx context.Context, db *sqlx.DB, accountId, planEventId string) (alertEventState, error) {
	var s alertEventState
	err := db.GetContext(ctx, &s, `SELECT ca.tenant::text AS tenant_id, COALESCE(e.nb_status, '') AS nb_status, e.nb_status_changed_at,
		       COALESCE(CASE WHEN jsonb_typeof(e.labels) = 'object' THEN e.labels->'nb_resolved_by' END, '{}'::jsonb)::text AS resolved_by
		  FROM events e JOIN cloud_accounts ca ON ca.id = e.cloud_account_id
		 WHERE e.id = $1::uuid AND e.cloud_account_id = $2::uuid`, planEventId, accountId)
	if err != nil {
		return s, fmt.Errorf("readAlertEventState: %w", err)
	}
	return s, nil
}

// readThreadLoop is the plan event's loop state, as the tab reads it. A var so tests can stub it.
var readThreadLoop = func(ctx *security.RequestContext, accountId, planEventId string, info *events.EventInfo, plan RemediationPlan) *remediationLoopState {
	if body, ok := remediationGetBody(ctx, accountId, planEventId, info, planEventId, info, plan).(remediationGetResponse); ok {
		return body.Loop
	}
	return nil
}

// resolveThreadPlanEvent is the event whose plan eventId's thread shows (a duplicate's main event) and its info. A
// var so tests can stub it.
var resolveThreadPlanEvent = func(ctx *security.RequestContext, accountId, eventId string) (string, *events.EventInfo, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", nil, fmt.Errorf("resolveThreadPlanEvent: %w", err)
	}
	repo := events.NewEventAnalysisRepository(dbManager)
	info, err := repo.GetEventInfo(ctx, eventId, accountId)
	if err != nil || info == nil {
		return "", nil, fmt.Errorf("resolveThreadPlanEvent: event %s: %w", eventId, err)
	}
	planEvent, planInfo := planEventAndInfo(ctx, repo, accountId, eventId, info)
	return planEvent, planInfo, nil
}

// readAlertThreadView reads what eventId's threads show: its state number first, then the state.
func readAlertThreadView(ctx *security.RequestContext, db *sqlx.DB, accountId, eventId string) (alertThreadView, error) {
	view := alertThreadView{AccountId: accountId}
	readCtx, cancel := context.WithTimeout(requestGoContext(ctx), 2*decisionDBTimeout)
	defer cancel()
	planEvent, info, err := resolveThreadPlanEvent(ctx, accountId, eventId)
	if err != nil {
		return view, err
	}
	view.PlanEventId = planEvent
	view.MainFingerprint = firstNonEmpty(info.Fingerprint, planEvent)
	if view.Seq, err = nextThreadStateSeq(readCtx, db); err != nil {
		return view, err
	}
	state, err := readAlertEventState(readCtx, db, accountId, planEvent)
	if err != nil {
		return view, err
	}
	view.TenantId, view.NbStatus, view.ResolvedAt = state.TenantId, state.NbStatus, state.ChangedAt
	_ = json.Unmarshal([]byte(state.ResolvedBy), &view.ResolvedBy)
	view.Plan, view.PlanVersion, view.HasPlan = loadSlackPlan(ctx, accountId, planEvent)
	view.Loop = readThreadLoop(ctx, accountId, planEvent, info, view.Plan)
	if view.Runs, err = readThreadRuns(readCtx, db, planEvent); err != nil {
		return view, err
	}
	view.Threads = []string{view.MainFingerprint}
	followers, err := episodeFollowerThreads(readCtx, db, accountId, planEvent)
	if err != nil {
		ctx.GetLogger().Warn("remediation_slack: duplicates' threads not read", "error", err, "event_id", planEvent)
	}
	for _, fp := range followers {
		if fp != view.MainFingerprint {
			view.Threads = append(view.Threads, fp)
		}
	}
	view.Names = threadNames(ctx, view)
	return view, nil
}

// threadNames reads the display names of the people the thread names (approvers, deniers).
func threadNames(ctx *security.RequestContext, view alertThreadView) map[string]string {
	var ids []string
	for _, r := range view.Runs {
		for _, id := range []string{r.Data.ApprovedBy, r.ResolverId} {
			if looksLikeUUID(id) {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return map[string]string{}
	}
	names, err := userDisplayNames(ctx, uniqueStrings(ids))
	if err != nil {
		return map[string]string{}
	}
	return names
}

func looksLikeUUID(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// syncAlertThread draws eventId's alert threads again from the server's state, in the background, when the account's
// loop is posted to Slack. A var so tests can capture the hooks that call it.
var syncAlertThread = func(ctx *security.RequestContext, accountId, eventId string) {
	inAlertThreadsBackground(ctx, accountId, eventId, alertThreadPosts, true)
}

// inAlertThreadsBackground sends, in the background, what draw makes of eventId's view into each of its threads.
// stateful sends carry the view's state number (an edit of the state); a one-off report does not.
func inAlertThreadsBackground(ctx *security.RequestContext, accountId, eventId string, draw func(view alertThreadView) []threadPost, stateful bool) {
	if !remediationSlackOn(accountId) || strings.TrimSpace(eventId) == "" {
		return
	}
	detached := ctx.WithContext(context.WithoutCancel(ctx.GetContext()))
	threadPostsInFlight.Add(1)
	go func() {
		defer threadPostsInFlight.Done()
		logger := detached.GetLogger().With("account_id", accountId, "event_id", eventId)
		defer func() {
			if r := recover(); r != nil {
				logger.Error("remediation_slack: panic", "panic", r, "stack", string(debug.Stack()))
			}
		}()
		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			logger.Warn("remediation_slack: database unavailable", "error", err)
			return
		}
		view, err := readAlertThreadView(detached, dbManager.Db, accountId, eventId)
		if err != nil {
			logger.Warn("remediation_slack: thread state not read", "error", err)
			return
		}
		posts := draw(view)
		if len(posts) == 0 {
			return
		}
		for _, send := range alertThreadSends(view, posts, stateful) {
			sendToAlertThread(detached.GetContext(), logger, send)
		}
	}()
}

// alertThreadSends is posts as one send per thread of the episode.
func alertThreadSends(view alertThreadView, posts []threadPost, stateful bool) []threadPosts {
	var out []threadPosts
	for _, fp := range view.Threads {
		send := threadPosts{TenantId: view.TenantId, AccountId: view.AccountId, EventId: view.PlanEventId, Fingerprint: fp, Posts: posts}
		if stateful {
			send.StateSeq = view.Seq
		}
		if fp != view.MainFingerprint {
			send.MainFingerprint = view.MainFingerprint
		}
		out = append(out, send)
	}
	return out
}

// Message tokens: what a message's buttons act on.
const (
	threadPlanTokenPrefix = "remediation-plan:"
	threadRunTokenPrefix  = "remediation-run:"
)

// threadVerdictsShown bounds the verdict messages one sync draws (the newest).
const threadVerdictsShown = 3

// alertThreadPosts is the view drawn: the replaced plan's options, the current options, then the verdicts, oldest
// first.
func alertThreadPosts(view alertThreadView) []threadPost {
	var out []threadPost
	if view.HasPlan {
		if rev := view.Plan.Revision; rev != nil && rev.PreviousVersion != "" && rev.PreviousVersion != view.PlanVersion {
			out = append(out, threadPost{Key: optionsKey(view.PlanEventId, rev.PreviousVersion), Edit: true, OnlyIfPosted: true,
				Text: "### Fix options (replaced)\n_Replaced by newer fix options below._"})
		}
		out = append(out, optionsPost(view))
	}
	return append(out, verdictPosts(view)...)
}

func optionsKey(eventId, version string) string { return "options:" + eventId + ":" + version }

// threadState is the state the options message's footer and buttons follow, computed as the tab computes it.
type threadState struct {
	Resolved  bool
	Preparing bool
	Stale     bool
	CameBack  *refireAfterRun
	Busy      bool
	Web       []pendingApproval
	Slack     []pendingApproval
	Chosen    *alertThreadRun // the option under way on this plan (newest), nil for none
	Phase     string          // the chosen run's phase (runThreadPhase)
}

func threadStateOf(view alertThreadView) threadState {
	s := threadState{Resolved: strings.EqualFold(view.NbStatus, "RESOLVED")}
	if l := view.Loop; l != nil {
		s.Preparing = l.PlanStatus == PlanStatusPreparing
		s.Stale = l.Relevance != nil && l.Relevance.Stale
		s.CameBack = l.RefireAfterRun
		s.Busy = l.ConversationBusy
		for _, p := range l.PendingApprovals {
			if p.Origin == ApprovalOriginSlack {
				s.Slack = append(s.Slack, p)
			} else {
				s.Web = append(s.Web, p)
			}
		}
	}
	count := len(view.Plan.Actions)
	for i := range view.Runs {
		r := &view.Runs[i]
		d := r.Data
		if option := d.option(); option < 1 || option > count || (d.PlanVersion != "" && d.PlanVersion != view.PlanVersion) {
			continue
		}
		if d.Slot == RemediationSlotRollback || r.Status == events.RunStatusDeclined || d.NotSent {
			continue
		}
		if r.Type == "PullRequest" && strings.EqualFold(r.PrLifecycle, "closed") {
			continue
		}
		s.Chosen = r // runs are oldest first: the newest wins
	}
	if s.Chosen != nil {
		s.Phase = runThreadPhase(*s.Chosen)
	}
	return s
}

// picking: a person can pick a fix now, as on the tab (RemediationLoopPanel picking): a plan with options, nothing
// under way, not resolved, not stale, not back, no approval waiting; and the alert's conversation not mid-turn.
func (s threadState) picking(view alertThreadView) bool {
	return view.HasPlan && len(view.Plan.Actions) > 0 && s.Chosen == nil && !s.Resolved && !s.Preparing && !s.Stale &&
		s.CameBack == nil && len(s.Web) == 0 && len(s.Slack) == 0 && !s.Busy
}

// Run phases, as the tab's runPhase names them.
const (
	phaseRunning      = "running"
	phaseChecking     = "checking"
	phaseVerifying    = "verifying"
	phaseFixed        = "fixed"
	phaseClearedFirst = "cleared_before_change"
	phaseNotFixed     = "not_fixed"
	phaseUnclear      = "unclear"
	phaseNeedsHuman   = "needs_human"
	phaseChecked      = "checked"
	phasePROpen       = "pr_open"
	phasePRMerged     = "pr_merged"
)

func runThreadPhase(r alertThreadRun) string {
	if r.Type == "PullRequest" {
		if strings.EqualFold(r.PrLifecycle, "merged") {
			return phasePRMerged
		}
		return phasePROpen
	}
	f := r.Data.Followup
	switch {
	case f != nil && f.Verdict != "":
		return f.Verdict
	case r.Status == events.RunStatusFailed:
		return phaseNotFixed
	case f != nil && f.DoneAt != "":
		if f.Outcome == followupNeedsHuman {
			return phaseNeedsHuman
		}
		return phaseChecked
	case r.Status != events.RunStatusSuccess && r.Status != events.RunStatusUnknown:
		return phaseRunning
	case f != nil && (f.HoldUntil != "" || f.RecheckAt != ""):
		return phaseVerifying
	}
	return phaseChecking
}

// phaseOver: the chosen run ended without fixing it, so a person decides what to try next.
func phaseOver(phase string) bool {
	switch phase {
	case phaseNotFixed, phaseUnclear, phaseNeedsHuman, phaseChecked:
		return true
	}
	return false
}

// clock is a time as the thread says it.
func clock(t time.Time) string { return t.UTC().Format("15:04") + " UTC" }

func clockOf(value string) string {
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return clock(t)
	}
	return ""
}

// optionTitle is an option's name.
func optionTitle(a RemediationAction) string {
	return strings.TrimSpace(firstNonEmpty(a.Title, a.Action))
}

// Bounds on the fix-options message: Slack shows a long section's text behind "Show more", so the cause and each
// option's line stay short; at most optionsShown options, within Slack's 50 blocks a message.
const (
	optionsCauseChars = 280
	optionLineChars   = 220
	optionsShown      = 40
)

// optionsPost is the fix-options message: the cause in one line, one section per option (its number, title, kind and
// one line; while a person can pick, its button beside it), the status footer and, when a person decides what to try
// next, Other fixes….
func optionsPost(view alertThreadView) threadPost {
	plan := view.Plan
	state := threadStateOf(view)
	text := "### Fix options"
	if plan.Version > 0 {
		text += fmt.Sprintf(" · v%d", plan.Version)
	}
	if c := oneLine(plan.RootCause, optionsCauseChars); c != "" {
		text += "\n**Root cause:** " + c
	}
	if len(plan.Actions) == 0 {
		text += "\n\n_No option NudgeBee can carry out: a person decides the change._"
	}
	post := threadPost{Key: optionsKey(view.PlanEventId, view.PlanVersion), Edit: true, Text: text,
		Token:  fmt.Sprintf("%s%s:%s:%s:%s", threadPlanTokenPrefix, view.TenantId, view.AccountId, view.PlanEventId, view.PlanVersion),
		Status: optionsStatus(view, state)}
	// Buttons only where the tab offers them; every click is checked again on the server.
	picking := state.picking(view)
	after := planRunsAfter(plan)
	for i, a := range plan.Actions {
		if i == optionsShown {
			post.Sections = append(post.Sections, threadSection{Text: fmt.Sprintf("_%d more options are on the Remediation tab._", len(plan.Actions)-i)})
			break
		}
		runsAfter := -1
		if i < len(after) {
			runsAfter = after[i]
		}
		section := threadSection{Text: optionText(a, i, runsAfter)}
		if picking {
			var repos []optionRepo
			if view.Loop != nil {
				repos = view.Loop.OptionRepos
			}
			button := optionButton(a, i, repos, view.PlanEventId)
			section.Button = &button
		}
		post.Sections = append(post.Sections, section)
	}
	post.Buttons = []threadButton{}
	if picking || (state.Chosen != nil && phaseOver(state.Phase) && !state.Resolved && !state.Preparing &&
		len(state.Web) == 0 && len(state.Slack) == 0 && !state.Busy) {
		post.Buttons = append(post.Buttons, threadButton{Kind: "other", Label: "Other fixes…"})
	}
	return post
}

// optionButton is the button beside an option, by how it is carried out: Use this fix (NudgeBee runs it), Raise a PR
// (a change in a repository the workload is known to come from), Choose the repo (a change in a repository the
// person confirms or picks on the tab, which lists the connected ones), Mark done (a change a person makes).
func optionButton(a RemediationAction, index int, repos []optionRepo, eventId string) threadButton {
	switch pickMode(a) {
	case pickKindRun:
		return threadButton{Kind: "pick", Label: "Use this fix", Option: index + 1, Style: "primary"}
	case pickKindPR:
		if optionRepoOf(repos, index+1).Known == repoKnownWorkload {
			return threadButton{Kind: "pr", Label: "Raise a PR", Option: index + 1}
		}
		if url := eventPageUrl(eventId); url != "" {
			return threadButton{Kind: "link", Label: "Choose the repo in NudgeBee", Option: index + 1, Url: url}
		}
	}
	return threadButton{Kind: "manual", Label: "Mark done", Option: index + 1}
}

// optionKind is what an option is, in the words of the tab: it buys time (a mitigation), a change in the deployment
// source, a code change, another change in source, or a fix.
func optionKind(a RemediationAction) string {
	if strings.EqualFold(strings.TrimSpace(a.Kind), RemediationKindMitigation) {
		return "buys time"
	}
	if rc := a.RequiredChange; strings.TrimSpace(a.ExecuteCommand) == "" && rc != nil && strings.TrimSpace(rc.Change) != "" {
		switch where := strings.ToLower(strings.TrimSpace(rc.Where)); where {
		case "code":
			return "code"
		case "deployment_source":
			return "change in the deployment source"
		default:
			if label := sourceWhereLabels[where]; label != "" {
				return strings.ToLower(label)
			}
		}
	}
	return "fix"
}

// optionText is one option of the plan, numbered from 1: "N · title" with its kind (and the option it runs after),
// then one short line on what it changes, with its trade-off when the plan names one.
func optionText(a RemediationAction, index, after int) string {
	text := fmt.Sprintf("**%d · %s** · _%s_", index+1, oneLine(optionTitle(a), 150), optionKind(a))
	if after >= 0 {
		text += fmt.Sprintf(" · after option %d", after+1)
	}
	var line string
	switch rc := a.RequiredChange; {
	case a.Automation != nil:
		line = oneLine("Runs automation "+automationLine(*a.Automation), optionLineChars)
	case strings.TrimSpace(a.ExecuteCommand) != "":
		line = "`" + oneLine(a.ExecuteCommand, optionLineChars-2) + "`"
	case rc != nil && strings.TrimSpace(rc.Change) != "":
		line = strings.TrimSpace(rc.Change)
		if t := strings.TrimSpace(rc.Target); t != "" {
			line += " in " + t
		}
		if repo := strings.TrimSpace(rc.Repo); repo != "" {
			line += " (" + repo + ")"
		}
		line = oneLine(line, optionLineChars)
	case a.AlertRuleChange != nil:
		line = "Changes the alert rule."
	default:
		line = oneLine("A person makes this change: "+firstNonEmpty(a.DeliveryReason, "NudgeBee doesn't run it"), optionLineChars)
	}
	if t := oneLine(a.TradeOff, 160); t != "" {
		line += " · Heads-up: " + t
	}
	if source := sourceMarkdown(a.Source); source != "" {
		line += "\n" + source
	}
	return text + "\n" + line
}

// oneLine is text's first line, at most n bytes.
func oneLine(text string, n int) string {
	return truncate(strings.TrimSpace(strings.SplitN(strings.TrimSpace(text), "\n", 2)[0]), n)
}

// surfaceWords is where an approval was given, as the footer says it.
func surfaceWords(from string) string {
	switch from {
	case ApprovalOriginSlack:
		return "in Slack"
	case ApprovalOriginWeb:
		return "on the tab"
	}
	return "in the alert's chat"
}

// eventPageUrl links to the event in the app, or "" when the app's address is not an absolute one.
func eventPageUrl(eventId string) string {
	base := strings.TrimRight(config.Config.BaseUrl, "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return ""
	}
	return base + "/investigate?id=" + eventId
}

// slackLink is a link in Slack's own form, which notifications-server's markdown conversion keeps as it is.
func slackLink(url, label string) string {
	return "<" + strings.NewReplacer("|", "%7C", ">", "%3E").Replace(url) + "|" + label + ">"
}

// optionsStatus is the options message's footer: one line for where things are, and one for a re-fire.
func optionsStatus(view alertThreadView, s threadState) string {
	var lines []string
	add := func(line string) {
		if line != "" {
			lines = append(lines, line)
		}
	}
	switch {
	case s.Resolved:
		line := "**Resolved**"
		if name := strings.TrimSpace(view.ResolvedBy.ConfirmedByName); name != "" {
			line += " by " + name
		}
		if view.ResolvedAt != nil {
			line += " at " + clock(*view.ResolvedAt)
		}
		add(line + ".")
	case s.Preparing:
		add("**New fix options are being prepared.** They replace these when they are ready.")
	case len(s.Slack) > 0:
		p := s.Slack[0]
		line := "**Waiting for approval in Slack**, asked at " + clock(p.CreatedAt)
		if p.ThreadUrl != "" {
			line += " · " + slackLink(p.ThreadUrl, "open")
		}
		add(line + ".")
	case len(s.Web) > 0:
		line := "**Waiting for approval on the Remediation tab**, asked at " + clock(s.Web[0].CreatedAt)
		if url := eventPageUrl(view.PlanEventId); url != "" {
			line += " · " + slackLink(url, "open")
		}
		add(line + ". It is answered there.")
	case s.Chosen != nil:
		add(chosenLine(view, s))
	case s.Stale:
		reason := ""
		if view.Loop != nil && view.Loop.Relevance != nil {
			reason = strings.TrimSpace(view.Loop.Relevance.StaleReason)
		}
		if reason != "" {
			reason = ": " + reason
		}
		add("**These fixes are out of date**" + reason + ".")
	case s.Busy:
		add("**Nubi is working in this alert's conversation.** The buttons come back when it is done.")
	case s.picking(view):
		add("Pick a fix: *Use this fix* asks for approval in this thread before anything runs.")
	}
	if s.CameBack != nil {
		add(fmt.Sprintf("**The alert came back** at %s, %d min after the fix.", clock(s.CameBack.FiredAt), s.CameBack.MinutesAfter))
	}
	return strings.Join(lines, "\n")
}

// phaseText is a run phase as the footer says it.
func phaseText(r alertThreadRun, phase string) string {
	f := r.Data.Followup
	reason := ""
	if f != nil {
		reason = verdictReasonText[f.VerdictReason]
	}
	with := func(word string) string {
		if reason != "" {
			return word + ": " + reason
		}
		return word
	}
	switch phase {
	case phaseRunning:
		return "running"
	case phaseChecking:
		return "checking whether it worked"
	case phaseVerifying:
		if f != nil && f.HoldUntil != "" {
			if at := clockOf(f.HoldUntil); at != "" {
				return "verifying until " + at + ": the alert must stay clear until then"
			}
		}
		return "verifying that it holds"
	case phaseFixed:
		return with("likely fixed")
	case phaseClearedFirst:
		return with("cleared on its own")
	case phaseNotFixed:
		if r.Status == events.RunStatusFailed && reason == "" {
			return "not fixed: the change failed"
		}
		return with("not fixed")
	case phaseUnclear:
		return with("couldn't confirm")
	case phaseNeedsHuman:
		return "needs a person"
	case phaseChecked:
		return "checked by Nubi"
	case phasePROpen:
		return "pull request open"
	case phasePRMerged:
		return "pull request merged"
	}
	return phase
}

// chosenLine: "Option 2 · Roll back: approved by Asha in Slack at 14:02 · running".
func chosenLine(view alertThreadView, s threadState) string {
	r := *s.Chosen
	d := r.Data
	option := d.option()
	title := ""
	if option >= 1 && option <= len(view.Plan.Actions) {
		title = " · " + optionTitle(view.Plan.Actions[option-1])
	}
	who := strings.TrimSpace(firstNonEmpty(view.Names[d.ApprovedBy], view.Names[r.ResolverId]))
	how := "approved"
	switch {
	case d.Manual:
		how = "made"
	case r.Type == "PullRequest":
		how = "raised"
	}
	line := fmt.Sprintf("**Option %d%s:** %s", option, title, how)
	if who != "" {
		line += " by " + who
	}
	if !d.Manual && r.Type != "PullRequest" {
		line += " " + surfaceWords(d.ApprovedFrom)
	}
	line += " at " + clock(r.CreatedAt)
	if note := strings.TrimSpace(d.ApprovalNote); note != "" {
		line += ": “" + note + "”"
	}
	return line + " · " + phaseText(r, s.Phase)
}

// verdictPosts are the verdict messages of the newest runs judged likely fixed or cleared before the change.
func verdictPosts(view alertThreadView) []threadPost {
	state := threadStateOf(view)
	var judged []alertThreadRun
	var newest *alertThreadRun
	for i := range view.Runs {
		r := view.Runs[i]
		if r.Status == events.RunStatusDeclined || r.Data.NotSent {
			continue
		}
		newest = &view.Runs[i]
		if f := r.Data.Followup; f != nil && (f.Verdict == verdictFixed || f.Verdict == verdictClearedBeforeChange) {
			judged = append(judged, r)
		}
	}
	if len(judged) > threadVerdictsShown {
		judged = judged[len(judged)-threadVerdictsShown:]
	}
	out := make([]threadPost, 0, len(judged))
	for _, r := range judged {
		f := r.Data.Followup
		head := "**Likely fixed:** "
		if f.Verdict == verdictClearedBeforeChange {
			head = "**Cleared before the change:** "
		}
		reason := firstNonEmpty(verdictReasonText[f.VerdictReason], "the alert's own firings say so")
		post := threadPost{Key: "verdict:" + r.Id, Edit: true,
			Token: fmt.Sprintf("%s%s:%s:%s:%s", threadRunTokenPrefix, view.TenantId, view.AccountId, view.PlanEventId, r.Id),
			Text:  head + reason + "."}
		post.Buttons = []threadButton{}
		refire := state.CameBack != nil && state.CameBack.RunId == r.Id
		switch {
		case state.Resolved && view.ResolvedBy.RunId == r.Id:
			line := "**Resolved**"
			if name := strings.TrimSpace(view.ResolvedBy.ConfirmedByName); name != "" {
				line += " by " + name
			}
			if view.ResolvedAt != nil {
				line += " at " + clock(*view.ResolvedAt)
			}
			post.Status = line + "."
		case state.Resolved:
			post.Status = "The event was resolved."
		case refire:
			post.Status = fmt.Sprintf("**The alert came back** at %s, so this fix can't be confirmed.", clock(state.CameBack.FiredAt))
		case newest != nil && newest.Id != r.Id:
			post.Status = "A later change replaced this one."
		default:
			post.Text += " Resolve to close the event in your name."
			post.Buttons = []threadButton{{Kind: "resolve", Label: "Resolve", RunId: r.Id, Style: "primary"}}
		}
		out = append(out, post)
	}
	return out
}

// --- hooks that are not a single call ------------------------------------------------------------------------

// syncAlertThreadOfSession draws the thread of the alert a conversation (event-<alert>) belongs to: its newest event
// with that fingerprint (or that id) on the account.
func syncAlertThreadOfSession(ctx *security.RequestContext, accountId, session string) {
	alert, ok := strings.CutPrefix(session, events.SessionIdPrefixEvent)
	if !ok || alert == "" || strings.HasPrefix(session, "event-rca-") || !remediationSlackOn(accountId) {
		return
	}
	eventId, err := newestEventOfAlert(ctx, accountId, alert)
	if err != nil || eventId == "" {
		return
	}
	syncAlertThread(ctx, accountId, eventId)
}

// newestEventOfAlert is the newest event on the account with this fingerprint, or with this id. A var so tests can
// stub it.
var newestEventOfAlert = func(ctx *security.RequestContext, accountId, alert string) (string, error) {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return "", fmt.Errorf("newestEventOfAlert: %w", err)
	}
	readCtx, cancel := context.WithTimeout(requestGoContext(ctx), decisionDBTimeout)
	defer cancel()
	var ids []string
	if err := dbManager.Db.SelectContext(readCtx, &ids, `SELECT id::text FROM events
		 WHERE cloud_account_id = $1::uuid AND (fingerprint = $2 OR id::text = $2)
		 ORDER BY created_at DESC LIMIT 1`, accountId, alert); err != nil {
		return "", fmt.Errorf("newestEventOfAlert: %w", err)
	}
	if len(ids) == 0 {
		return "", nil
	}
	return ids[0], nil
}

// resolvedVerdictRun is a run judged likely fixed whose event was resolved after the thread last drew it.
type resolvedVerdictRun struct {
	RunId     string `db:"run_id"`
	EventId   string `db:"event_id"`
	AccountId string `db:"account_id"`
}

// resolvedSinceThreadDrawn lists, for the follow-ups tick, the runs of the last 7 days judged likely fixed (or cleared
// before the change) whose event became RESOLVED after the run's thread_synced_at (or its verdict): resolved on the
// tab, which tells the thread nothing itself. At most 20. A var so tests can stub it.
var resolvedSinceThreadDrawn = func(ctx context.Context, db *sqlx.DB) ([]resolvedVerdictRun, error) {
	var rows []resolvedVerdictRun
	err := db.SelectContext(ctx, &rows, `SELECT r.id::text AS run_id, e.id::text AS event_id, e.cloud_account_id::text AS account_id
		  FROM event_resolution r JOIN events e ON e.id = r.event_id
		 WHERE r.type = 'CommandExecution' AND r.created_at > LOCALTIMESTAMP - interval '7 days'
		   AND r.data->'followup'->>'verdict' IN ('fixed', 'cleared_before_change')
		   AND e.nb_status = 'RESOLVED' AND e.nb_status_changed_at IS NOT NULL
		   AND e.nb_status_changed_at > COALESCE(NULLIF(r.data->>'thread_synced_at', '')::timestamp, r.created_at)
		 ORDER BY e.nb_status_changed_at DESC LIMIT 20`)
	if err != nil {
		return nil, fmt.Errorf("resolvedSinceThreadDrawn: %w", err)
	}
	return rows, nil
}

// markThreadSynced records when the run's thread was drawn after a resolve, so the tick draws it once. A var so
// tests can stub it.
var markThreadSynced = func(ctx context.Context, db *sqlx.DB, runIds []string) error {
	if len(runIds) == 0 {
		return nil
	}
	_, err := db.ExecContext(ctx, `UPDATE event_resolution SET data = COALESCE(data, '{}'::jsonb) || jsonb_build_object('thread_synced_at', LOCALTIMESTAMP::text)
		 WHERE id = ANY($1::uuid[])`, pq.Array(runIds))
	if err != nil {
		return fmt.Errorf("markThreadSynced: %w", err)
	}
	return nil
}

// syncResolvedThreads is the tick's reconciliation of a resolve made on the tab (api-server does not call
// notifications-server): each such alert's thread is drawn once, at most a tick late. Idempotent: the run is marked
// once drawn, and drawing twice changes nothing.
func syncResolvedThreads(db *sqlx.DB) {
	ctx, cancel := context.WithTimeout(context.Background(), decisionDBTimeout)
	defer cancel()
	rows, err := resolvedSinceThreadDrawn(ctx, db)
	if err != nil {
		ctx2 := security.NewRequestContextForSuperAdmin()
		ctx2.GetLogger().Warn("remediation_followups: resolved alerts not read", "error", err)
		return
	}
	var marked []string
	seen := map[string]bool{}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].EventId < rows[j].EventId })
	for _, r := range rows {
		marked = append(marked, r.RunId)
		if seen[r.EventId] || !remediationSlackOn(r.AccountId) {
			continue
		}
		seen[r.EventId] = true
		syncAlertThread(security.NewRequestContextForSuperAdmin(), r.AccountId, r.EventId)
	}
	if err := markThreadSynced(ctx, db, marked); err != nil {
		security.NewRequestContextForSuperAdmin().GetLogger().Warn("remediation_followups: resolved alerts not marked", "error", err)
	}
}
