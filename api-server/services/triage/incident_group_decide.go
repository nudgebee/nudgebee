package triage

import (
	"fmt"
	"time"
)

// incident_group_decide.go is the decision half of incident grouping: given the
// alert being triaged (the seed) and the alerts active around it, which incident
// does the seed belong to and who heads it. Pure — incident_group.go loads the
// inputs and writes the result.
//
// The decision is made over ALERTS, not event rows, and over everything active
// around the seed, not only what started before it. Deciding per row, looking
// strictly backwards, made the answer depend on arrival order: three alerts
// starting in the same second never saw each other, a cause arriving after its
// symptoms was filed under one of them, and a second copy of an alert from
// another ingest path could be picked in place of the copy that carried the link.

// alertMember is one alert in the seed's active set: every stored row of one
// fingerprint, collapsed.
type alertMember struct {
	// Fingerprint is the alert's identity. Rows without one stand alone, keyed by
	// their own id.
	Fingerprint string
	// RepRow is the row a link points at when this alert heads a group: the
	// earliest-stored row of its first firing inside the attach window, or of its
	// current firing when that started before the window.
	RepRow string
	// Rows is this alert in the incident: the rows of its current firing and its
	// rows inside the attach window. Its existing link is looked for on these,
	// and these are what another alert's triage files along with it. An older
	// firing is history — its link says where the alert was, not where it is.
	Rows           []string
	AggregationKey string
	SubjectKey     string
	ServiceKey     string
	// Subject and Namespace are the identity the firing-rate query matches on.
	Subject   string
	Namespace string
	// StartsAt is the start of the firing RepRow belongs to.
	StartsAt time.Time
	// Recent: the current firing started inside the attach window around the seed.
	Recent bool
	// Open: the current firing was still going when the seed started.
	Open bool
	// Resource is what the tag relations read off the resource a cloud alarm is
	// about (incident_group_app.go); empty for everything else.
	Resource resourceTags
}

// relation is the evidence that puts a member in the seed's incident, strongest
// first.
type relation int

const (
	relSameSubject relation = iota + 1
	relConnected
	relDependency
	relNode
	relSameApp
	relPatchWave
)

// relatedMember is an active alert the seed has evidence for.
type relatedMember struct {
	alertMember
	Relation relation
	// Hops is the service-map distance for relConnected and relDependency.
	Hops int
	// GroupHead is the head row of the group this alert is already in — its own
	// leading row when it heads one, its leader's row when it is filed under one —
	// and "" when it is in none.
	GroupHead string
}

// decideInput is everything the decision reads.
type decideInput struct {
	// Seed is the alert being triaged; Seed.RepRow is the seed row itself.
	Seed    alertMember
	SeedKey string
	// Where the seed's alert already stands — from the other rows of this firing
	// (another ingest path's copy, a re-notification), or from an earlier firing
	// inside the window whose group another alert is still keeping open: OwnHead
	// is the head row it is filed under, OwnLeads the row of the alert that heads
	// its group. OwnRep is the row that stands for the alert if it comes to head
	// a new group and that is not the seed row: the earliest other row of this
	// firing, or of an earlier firing in the window that is in no group.
	OwnHead  string
	OwnLeads string
	OwnRep   string

	Related []relatedMember
	// HeadStarts is the start of every head row named in Related, and HeadAlerts
	// the alert (fingerprint) each of those rows belongs to.
	HeadStarts map[string]time.Time
	HeadAlerts map[string]string
	// Noisy and DependedOnBy are keyed by fingerprint, the seed's included.
	Noisy        map[string]bool
	DependedOnBy map[string]int
	// TakeoverEnabled is the kill switch for replacing an existing head.
	TakeoverEnabled bool
	// Node is the node the node-memory relation is about, for the link's reason.
	Node string
}

type groupOutcome int

const (
	// outcomeNone: nothing to group the seed with.
	outcomeNone groupOutcome = iota
	// outcomeJoin: link the seed row to HeadRow.
	outcomeJoin
	// outcomeLead: the seed's alert heads the group. HeadRow is the row to link
	// the seed row to when another row of the same alert already carries the
	// lead, and "" when the seed row is that row.
	outcomeLead
	// outcomeTakeOver: the seed replaces the head MoveFrom; that head's whole
	// star moves to the seed row.
	outcomeTakeOver
)

// groupDecision is the plan the caller applies.
type groupDecision struct {
	Outcome     groupOutcome
	HeadRow     string
	MoveFrom    string
	Reason      string
	DepDistance int
	// SeedIsChild is what scoring is told: the seed is a symptom filed under
	// another alert. A further row of the head's own alert is not.
	SeedIsChild bool
	// HeadStart is the head row's start, for the link's offset. Zero when unknown.
	HeadStart time.Time
	// Bring lists rows of related alerts that are in no group yet; they are linked
	// to the same head as the seed. An alert triaged before anything related to
	// it existed is not triaged again, so the alert that finds it has to file it.
	Bring []bringRow
	// Why is a one-line account of the decision for the log.
	Why string
}

// bringRow is one row of a related, ungrouped alert to link to the decided head.
type bringRow struct {
	Row    string
	Reason string
	Starts time.Time
}

// rankKey orders alerts for the lead.
//
// A noisy alert never heads while a quieter one is there: a flapper must not be
// the headline of an incident it merely accompanies. Among the rest the alert the
// most others depend on leads — an alert on a thing its neighbours need is the
// cause, and the alerts on the things that need it are the symptoms. Start time
// and then the fingerprint only break ties nothing structural separates, so the
// choice is the same whichever alert happens to be triaged first.
type rankKey struct {
	noisy        bool
	dependedOnBy int
	start        time.Time
	fingerprint  string
}

func (in decideInput) rank(m alertMember) rankKey {
	return rankKey{
		noisy:        in.Noisy[m.Fingerprint],
		dependedOnBy: in.DependedOnBy[m.Fingerprint],
		start:        m.StartsAt,
		fingerprint:  m.Fingerprint,
	}
}

// better is the full order: a is the better head.
func (a rankKey) better(b rankKey) bool {
	if a.noisy != b.noisy {
		return !a.noisy
	}
	if a.dependedOnBy != b.dependedOnBy {
		return a.dependedOnBy > b.dependedOnBy
	}
	if !a.start.Equal(b.start) {
		return a.start.Before(b.start)
	}
	return a.fingerprint < b.fingerprint
}

// outranks is the structural part only: quieter, or depended on by strictly
// more. It is what an existing head has to lose on to be replaced — a tie on
// timing alone never moves a group.
func (a rankKey) outranks(b rankKey) bool {
	if a.noisy != b.noisy {
		return !a.noisy
	}
	return a.dependedOnBy > b.dependedOnBy
}

// decideGroup places the seed.
//
//  1. A firing that is already placed stays where it is. If another row of the
//     seed's firing is filed under a head, the seed follows it; if the seed's
//     alert heads the group — this firing, or an earlier one heading a group a
//     related alert is in — the seed is one more row of the head. This is also
//     what stops the lead passing back and forth: each seed ranks with its own
//     stored service map, two maps can disagree, and only an alert new to a
//     group may change who heads it.
//
//     An EARLIER firing of the seed's alert places it only while another
//     alert in that group has fired inside the window. A group stays open while
//     different alerts keep firing beside each other; one alert's own repeats do
//     not hold it open, or a flapper would keep an incident alive for days under
//     a headline long out of view.
//
//  2. If related alerts are already in groups, the seed joins the group whose
//     head ranks best. It replaces that head only when it outranks it. Groups are
//     never merged.
//
//  3. With no group yet, the head is the best-ranked of the seed and its related
//     alerts. Every alert triaged from the same set reaches the same answer.
//
// Whatever the outcome, related alerts that are in no group come along (Bring).
func decideGroup(in decideInput) groupDecision {
	d := in.decide()
	if d.Outcome == outcomeNone {
		return d
	}
	head := d.HeadRow
	if d.Outcome == outcomeLead && head == "" {
		head = in.Seed.RepRow
	}
	for _, m := range in.Related {
		if m.GroupHead != "" {
			continue
		}
		reason := fmt.Sprintf("%s is related to %s, both firing within the incident attach window", m.SubjectKey, in.SeedKey)
		switch m.Relation {
		case relNode:
			// The node axis audits and guards its links by this prefix.
			reason, _ = in.linkReason(m)
		case relSameApp, relPatchWave:
			// So do the tag relations; the reason names the alert being filed.
			reason = resourceReason(m.Relation, m.Resource, m.SubjectKey)
		}
		for _, row := range m.Rows {
			if row != head {
				d.Bring = append(d.Bring, bringRow{Row: row, Reason: reason, Starts: m.StartsAt})
			}
		}
	}
	return d
}

func (in decideInput) decide() groupDecision {
	if in.OwnHead != "" {
		return groupDecision{
			Outcome: outcomeJoin, HeadRow: in.OwnHead, SeedIsChild: true,
			HeadStart: in.HeadStarts[in.OwnHead],
			Reason:    fmt.Sprintf("%s is already in this incident: another firing or copy of the same alert", in.SeedKey),
			Why:       "own alert already filed under this head",
		}
	}
	if in.OwnLeads != "" {
		return groupDecision{
			Outcome: outcomeLead, HeadRow: in.OwnLeads,
			HeadStart: in.HeadStarts[in.OwnLeads],
			Reason:    fmt.Sprintf("%s leads this incident: another firing or copy of the same alert", in.SeedKey),
			Why:       "own alert already heads this group",
		}
	}
	if len(in.Related) == 0 {
		return groupDecision{Outcome: outcomeNone, Why: "no related alert active"}
	}

	seedRank := in.rank(in.Seed)

	// Existing groups among the related alerts. A head that is itself one of the
	// related alerts has a rank; a head from outside the active set does not.
	type group struct {
		head    string
		ranked  bool
		rank    rankKey
		via     relatedMember // strongest-related member that reaches this group
		hasVia  bool
		headFP  string
		started time.Time
	}
	groups := map[string]*group{}
	var order []string
	for _, m := range in.Related {
		if m.GroupHead == "" {
			continue
		}
		g, ok := groups[m.GroupHead]
		if !ok {
			g = &group{head: m.GroupHead, started: in.HeadStarts[m.GroupHead], headFP: in.HeadAlerts[m.GroupHead]}
			groups[m.GroupHead] = g
			order = append(order, m.GroupHead)
		}
		if !g.hasVia || m.Relation < g.via.Relation ||
			(m.Relation == g.via.Relation && m.Fingerprint < g.via.Fingerprint) {
			g.via, g.hasVia = m, true
		}
		for _, row := range m.Rows {
			if row == m.GroupHead {
				g.ranked, g.rank, g.headFP = true, in.rank(m.alertMember), m.Fingerprint
			}
		}
	}
	// A related alert sits in a group headed by an earlier firing of the seed's
	// own alert, one that started before the window: the seed is one more row of
	// that head, not a symptom of it.
	var own *group
	for _, h := range order {
		g := groups[h]
		if g.headFP != in.Seed.Fingerprint {
			continue
		}
		if own == nil || g.started.Before(own.started) || (g.started.Equal(own.started) && g.head < own.head) {
			own = g
		}
	}
	if own != nil {
		return groupDecision{
			Outcome: outcomeLead, HeadRow: own.head, HeadStart: own.started,
			Reason: fmt.Sprintf("%s leads this incident: another firing or copy of the same alert", in.SeedKey),
			Why:    "an earlier firing of this alert heads a related alert's group",
		}
	}
	if len(order) > 0 {
		best := groups[order[0]]
		for _, h := range order[1:] {
			g := groups[h]
			switch {
			case g.ranked != best.ranked:
				if g.ranked {
					best = g
				}
			case g.ranked && g.rank.better(best.rank):
				best = g
			case !g.ranked && (g.started.Before(best.started) || (g.started.Equal(best.started) && g.head < best.head)):
				best = g
			}
		}
		if in.TakeoverEnabled && best.ranked && seedRank.outranks(best.rank) {
			return groupDecision{
				Outcome: outcomeTakeOver, HeadRow: in.Seed.RepRow, MoveFrom: best.head,
				HeadStart:   best.started,
				DepDistance: best.via.Hops,
				Reason: fmt.Sprintf("%s %d alert(s) in this incident depend on %s",
					leaderTakeoverReasonPrefix, seedRank.dependedOnBy, in.SeedKey),
				Why: fmt.Sprintf("outranks the head (noisy %t/%t, depended on by %d/%d)",
					seedRank.noisy, best.rank.noisy, seedRank.dependedOnBy, best.rank.dependedOnBy),
			}
		}
		reason, dist := in.linkReason(best.via)
		return groupDecision{
			Outcome: outcomeJoin, HeadRow: best.head, SeedIsChild: true,
			HeadStart: best.started, Reason: reason, DepDistance: dist,
			Why: fmt.Sprintf("joins an existing group (%d group(s) among related alerts)", len(order)),
		}
	}

	// No group yet: elect over the seed and its related alerts.
	head, isSeed := in.Seed, true
	headRank := seedRank
	var via relatedMember
	for _, m := range in.Related {
		if r := in.rank(m.alertMember); r.better(headRank) {
			head, headRank, isSeed, via = m.alertMember, r, false, m
		}
	}
	if isSeed {
		// Another row of the seed's alert may already stand for it.
		return groupDecision{
			Outcome: outcomeLead, HeadRow: in.OwnRep, HeadStart: in.HeadStarts[in.OwnRep],
			Reason: fmt.Sprintf("%s leads this incident: another firing or copy of the same alert", in.SeedKey),
			Why:    "best-ranked of the active set",
		}
	}
	reason, dist := in.linkReason(via)
	return groupDecision{
		Outcome: outcomeJoin, HeadRow: head.RepRow, SeedIsChild: true,
		HeadStart: head.StartsAt, Reason: reason, DepDistance: dist,
		Why: "new group; head is the best-ranked of the active set",
	}
}

// linkReason states the evidence that put the seed in the group, through the
// member it was reached by, and the dependency distance to record with it.
func (in decideInput) linkReason(via relatedMember) (string, int) {
	switch via.Relation {
	case relSameSubject:
		return fmt.Sprintf("same subject (%s) within incident attach window", in.SeedKey), 0
	case relConnected:
		return fmt.Sprintf("%s is %d hop(s) away, both firing within the incident attach window", in.SeedKey, via.Hops), via.Hops
	case relDependency:
		return fmt.Sprintf("%s depends on %s, which was still firing when this alert started", in.SeedKey, via.SubjectKey), via.Hops
	case relNode:
		return fmt.Sprintf("%s node %s ran out of memory; %s failed on it within the incident attach window", nodeMemoryReasonPrefix, in.Node, in.SeedKey), 0
	case relSameApp, relPatchWave:
		return resourceReason(via.Relation, via.Resource, in.SeedKey), 0
	}
	return fmt.Sprintf("%s within incident attach window", in.SeedKey), 0
}
