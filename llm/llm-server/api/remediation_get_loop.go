package api

import (
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
)

// The remediation read (processRemediationGet) also returns the alert's loop state when the
// remediation loop is on: whether a plan exists or is being written, whether it still applies,
// which approvals are waiting, and which event a duplicate's remediation runs on. Three hooks
// fill it, each in its own file, so the plan, approval and resolve work can change them
// independently: planStatusAndRelevance, pendingApprovals, primaryEventOf.

// Plan status values.
const (
	PlanStatusReady     = "ready"
	PlanStatusPreparing = "preparing"
	PlanStatusNone      = "none"
)

// Approval origins: where the request that raised the approval was made.
const (
	ApprovalOriginWeb   = "web"
	ApprovalOriginSlack = "slack"
)

// remediationLoopState is the "loop" object of the remediation read.
type remediationLoopState struct {
	PlanStatus       string            `json:"plan_status"`
	PlanStatusReason string            `json:"plan_status_reason,omitempty"`
	PreparingSince   *time.Time        `json:"preparing_since,omitempty"`
	Relevance        *planRelevance    `json:"relevance,omitempty"`
	PendingApprovals []pendingApproval `json:"pending_approvals,omitempty"`
	// PendingApprovalsUnread: the waiting approvals could not be read this time, so pending_approvals
	// says nothing about them; the tab keeps what it last showed.
	PendingApprovalsUnread bool `json:"pending_approvals_unread,omitempty"`
	// DeniedApprovals are the plan steps a person denied in this alert's conversation, newest first,
	// at most 10 from the last 12 h: a denial writes no run record, so this is how the tab shows it.
	DeniedApprovals []deniedApproval `json:"denied_approvals,omitempty"`
	// DeniedApprovalsUnread: the denials could not be read this time; the tab keeps what it last showed.
	DeniedApprovalsUnread bool   `json:"denied_approvals_unread,omitempty"`
	PrimaryEventId        string `json:"primary_event_id,omitempty"`
	// PrimaryFingerprint is the main event's fingerprint, so a duplicate opens the main event's
	// conversation (event-<fingerprint>) even when the two fingerprints differ.
	PrimaryFingerprint string `json:"primary_fingerprint,omitempty"`
	// LastRequestAt and LastRequestMode are the newest planning request for the plan's event: when it was made and
	// what it asked (auto, ask, recheck, replan). With a plan shown and that request ended with no plan,
	// PlanStatusReason says why.
	LastRequestAt   *time.Time `json:"last_request_at,omitempty"`
	LastRequestMode string     `json:"last_request_mode,omitempty"`
	// ConversationBusy: the alert's conversation (event-<plan fingerprint>) has a turn in progress or waiting.
	ConversationBusy bool `json:"conversation_busy"`
	// InvestigationAfterPlan is set when the alert's newest completed investigation finished after the plan was
	// written; ParkedRecheck while the re-check it asked for is parked behind a waiting approval.
	InvestigationAfterPlan *investigationAfterPlan `json:"investigation_after_plan,omitempty"`
	ParkedRecheck          bool                    `json:"parked_recheck,omitempty"`
	// Firings are the alert's firings from the plan's event on (its fingerprint and its duplicates), oldest first,
	// at most loopFiringsShown: all of them when they fit; else the first episode's start (its oldest
	// loopFirstEpisodeShown) and the firing that closed it (the first clear), then the newest. FiringsTotal counts
	// them all, so the tab can say how many it leaves out.
	Firings      []loopFiring `json:"firings,omitempty"`
	FiringsTotal int          `json:"firings_total"`
	// RefireAfterRun is set when the alert fired after the newest run's check ended (its follow-up, or its verdict).
	RefireAfterRun *refireAfterRun `json:"refire_after_run,omitempty"`
}

// loopFiringsShown bounds loop.firings; loopFirstEpisodeShown is how many of the first episode's oldest firings it
// keeps when the firings do not fit.
const (
	loopFiringsShown      = 20
	loopFirstEpisodeShown = 3
)

// loopFiring is one firing of the alert. EndsAt is null while it fires.
type loopFiring struct {
	EventId  string     `json:"event_id"`
	StartsAt time.Time  `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`
	Status   string     `json:"status"`
	NbStatus string     `json:"nb_status"`
}

// refireAfterRun is the first firing after the newest run's check ended: the run, the firing (its event and when it
// started) and how many minutes after the run it started. Cause compares the firing's cause with the run's plan,
// from the firing's nb_refire label: same_cause, different or unknown; empty without the label.
type refireAfterRun struct {
	RunId        string    `json:"run_id"`
	EventId      string    `json:"event_id"`
	FiredAt      time.Time `json:"fired_at"`
	MinutesAfter int       `json:"minutes_after"`
	Cause        string    `json:"cause,omitempty"`
}

// planRelevance says whether the stored plan still applies. Computed on every read, without a
// model call.
type planRelevance struct {
	AlertState       string     `json:"alert_state"` // firing | closed | resolved
	AlertStateAt     *time.Time `json:"alert_state_at,omitempty"`
	FiringsSincePlan int        `json:"firings_since_plan"`
	RunsSincePlan    int        `json:"runs_since_plan"`
	NewerEventId     string     `json:"newer_event_id,omitempty"`
	Stale            bool       `json:"stale"`
	StaleReason      string     `json:"stale_reason,omitempty"`
}

// pendingApproval is a write Nubi is waiting on a person to approve in this alert's conversation.
type pendingApproval struct {
	// ConversationId, MessageId and AgentId are what the existing follow-up answer needs.
	ConversationId string `json:"conversation_id"`
	MessageId      string `json:"message_id"`
	AgentId        string `json:"agent_id"`
	// ThreadUrl links to the Slack thread of a Slack-started approval, when it is known.
	ThreadUrl string    `json:"thread_url,omitempty"`
	Tool      string    `json:"tool"`
	Command   string    `json:"command"`
	CreatedAt time.Time `json:"created_at"`
	Origin    string    `json:"origin"` // web | slack
}

// deniedApproval is a plan step (run_step) a person denied in this alert's conversation.
type deniedApproval struct {
	MessageId   string `json:"message_id"`
	Tool        string `json:"tool"`
	Command     string `json:"command"`
	Option      int    `json:"option,omitempty"` // 1-based
	PlanVersion string `json:"plan_version"`
	// StepKey is the step's own key (events.ApprovalKey of the step), as a run of it carries in
	// data.step_key; set for an apply or rollback whose command is known.
	StepKey  string    `json:"step_key,omitempty"`
	DeniedAt time.Time `json:"denied_at"`
	// DeniedBy is the person who answered, when recorded with the answer.
	DeniedBy string `json:"denied_by,omitempty"`
	// DeniedByName is that person's display name, when it can be read.
	DeniedByName string `json:"denied_by_name,omitempty"`
	// Reason is set when NudgeBee closed the approval unanswered instead of a person denying it: "lapsed" (nobody
	// answered in time) or "superseded" (the alert cleared and fired again after it was asked). Nothing ran.
	Reason string `json:"reason,omitempty"`
	// AskedAt is when the approval was asked; AskedBy the person whose turn asked it, AskedByName their name.
	AskedAt     time.Time `json:"asked_at"`
	AskedBy     string    `json:"asked_by,omitempty"`
	AskedByName string    `json:"asked_by_name,omitempty"`
}

// remediationGetInput is what every hook gets. EventId is the event the page asked about; PlanEventId and
// PlanFingerprint name the event whose plan the read returns: the main event for a duplicate, the event
// itself otherwise.
type remediationGetInput struct {
	AccountId       string
	EventId         string
	Fingerprint     string
	PlanEventId     string
	PlanFingerprint string
	Plan            *RemediationPlan
}

// remediationGetResponse keeps the plan's fields at the top level, as before, and adds "loop".
type remediationGetResponse struct {
	RemediationPlan
	Loop *remediationLoopState `json:"loop,omitempty"`
}

// remediationGetBody is the body of the remediation read: the plan alone when the loop is off
// (the response is unchanged), and the plan plus its loop state when it is on.
func remediationGetBody(ctx *security.RequestContext, accountId, eventId string, info *events.EventInfo, planEventId string, planInfo *events.EventInfo, plan RemediationPlan) any {
	if info == nil || !events.RemediationLoopOn(accountId) {
		return plan
	}
	if planEventId == "" || planInfo == nil {
		planEventId, planInfo = eventId, info
	}
	in := remediationGetInput{AccountId: accountId, EventId: eventId, Fingerprint: info.Fingerprint,
		PlanEventId: planEventId, PlanFingerprint: planInfo.Fingerprint, Plan: &plan}
	loop := &remediationLoopState{}
	// The waiting approvals do not depend on the rest, so they are read alongside it.
	type pendingRead struct {
		approvals []pendingApproval
		read      bool
	}
	pending := make(chan pendingRead, 1)
	go func() {
		approvals, read := pendingApprovals(ctx, in)
		pending <- pendingRead{approvals, read}
	}()
	type deniedRead struct {
		denials []deniedApproval
		read    bool
	}
	denied := make(chan deniedRead, 1)
	go func() {
		denials, read := deniedApprovals(ctx, in)
		denied <- deniedRead{denials, read}
	}()
	loop.PrimaryEventId = primaryEventOf(ctx, in)
	if loop.PrimaryEventId != "" && loop.PrimaryEventId != eventId {
		loop.PrimaryFingerprint = eventFingerprintOf(ctx, accountId, loop.PrimaryEventId)
	}
	planStatusAndRelevance(ctx, in, loop)
	p := <-pending
	loop.PendingApprovals, loop.PendingApprovalsUnread = p.approvals, !p.read
	d := <-denied
	loop.DeniedApprovals, loop.DeniedApprovalsUnread = d.denials, !d.read
	return remediationGetResponse{RemediationPlan: plan, Loop: loop}
}

// eventFingerprintOf returns an event's fingerprint, or "" when it cannot be read. A var so tests can
// stub it.
var eventFingerprintOf = func(ctx *security.RequestContext, accountId, eventId string) string {
	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return ""
	}
	info, err := events.NewEventAnalysisRepository(dbManager).GetEventInfo(ctx, eventId, accountId)
	if err != nil || info == nil {
		return ""
	}
	return info.Fingerprint
}

// investigationAfterPlan is when the investigation newer than the plan finished.
type investigationAfterPlan struct {
	At time.Time `json:"at"`
}
