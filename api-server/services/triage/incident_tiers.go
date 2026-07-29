package triage

import (
	"regexp"
	"sort"
	"strings"
)

// incident_tiers.go is the deterministic four-tier incident assembly (#34658,
// Step 3). Given the alert a viewer opened (the seed) and the surrounding window
// of alerts, plus who-calls-whom (from the knowledge-graph walk) and each alert's
// trailing firing rate (from the rarity query), AssembleTiers sorts every other
// alert into one tier relative to the seed:
//
//	core    — same subject as the seed: the incident's own alerts.
//	cause   — a deploy/config change or an upstream alert in the lead-up window.
//	impact  — a downstream dependent alerting at/after the incident, when that is
//	          rare for it.
//	chronic — a would-be core/impact member that fires so often it was going to be
//	          in any window; folded as background noise, never shown as related.
//
// It is pure (no I/O): the caller (api.handleEventGetImpact) fetches the window,
// resolves the topology, and runs the rarity query, then calls this. The replay
// harness (triage/replay) scores this exact function against labelled incidents,
// so the gate measures production logic. See docs/incident-assembly-spec.md.
//
// Scope: workload subjects. Database-family grouping (the SubjectKey datastore
// branch) is staged but unwired in production — see the datastore-grouping
// follow-up; the chronic tier folds the constant datname chatter regardless.

// Tier names shared by the assembler, the API response, and the replay gate.
const (
	TierCore    = "core"
	TierCause   = "cause"
	TierImpact  = "impact"
	TierChronic = "chronic"
)

const (
	// CauseLeadInSeconds bounds the cause window to [root-2h, root+CauseLagGraceSeconds].
	CauseLeadInSeconds = -2 * 60 * 60
	// CauseLagGraceSeconds extends the cause window past the root, for alerts on an
	// upstream dependency only. A dependency's own alert routinely fires slightly AFTER
	// its caller's: the caller's client-side error-rate rule trips as soon as calls start
	// failing, while the callee's server-side rule needs a further evaluation interval to
	// cross threshold. With a strict "at or before the root" bound the actual cause fell
	// out of both lanes — it is not a dependent (so not impact) and not before the root
	// (so not cause) — and the incident rendered with an empty cause section. Config
	// changes keep the strict bound: a deploy after the root did not cause it.
	CauseLagGraceSeconds = 5 * 60
	// chronicExpectedMin: expected occurrences in the window at/above which an alert
	// "was going to be here anyway" and is folded as chronic.
	chronicExpectedMin = 1.0
	// chronicBurstFactor: observed occurrences above this multiple of expected mean
	// the subject is bursting far past its own baseline — a real incident on a noisy
	// subject — so it is NOT folded.
	chronicBurstFactor = 3.0
)

// podTemplateHashSuffix matches a trailing ReplicaSet/pod-template hash (e.g.
// "-5cbd8ddb97", "-6b9kfmn4p2"). Kubernetes encodes these with a vowel-free base32
// alphabet (k8s.io/apimachinery rand.SafeEncodeString: "bcdfghjklmnpqrstvwxz" +
// digits), so match that — not [a-f0-9] (misses hashes containing g/h/j/k/m/n/...)
// nor [a-z0-9] (would strip real trailing words like "-server"/"-worker").
var podTemplateHashSuffix = regexp.MustCompile(`-[bcdfghjklmnpqrstvwxz0-9]{6,10}$`)

// AlertIdentity is the minimal projection of an event the tiering rules key on.
type AlertIdentity struct {
	ID               string
	SubjectType      string
	SubjectName      string
	SubjectNamespace string
	SubjectOwner     string
	// Series is the sibling-datname set of a datastore alert (unwired in production;
	// see scope note above).
	Series         []string
	AggregationKey string
	// TsOffsetS is the alert's start offset from the incident root, in seconds
	// (negative = before the root).
	TsOffsetS int
	// IsConfigChange marks a deploy / configuration-change event (finding_type =
	// 'configuration_change').
	IsConfigChange bool
	// FindingType is the raw events.finding_type ('issue', 'Anomaly', 'SLO',
	// 'configuration_change'), used to keep derived signals out of the causal lanes.
	FindingType string
}

// isDerivedSignal reports whether an alert is a statistical/derived signal (an SLO
// violation or an anomaly detection) rather than a concrete failure. These fire
// broadly across services and make poor cross-service evidence — a CPU anomaly on
// some unrelated collector is not why your service OOMed — so they are never offered
// as a cause or an impact. They still appear under the seed's own subject, where they
// are legitimate context about the thing you opened.
func isDerivedSignal(a AlertIdentity) bool {
	switch strings.ToLower(a.FindingType) {
	case "slo", "anomaly":
		return true
	}
	return false
}

// Rate is an alert's trailing firing frequency relative to the incident window:
// Expected occurrences the history predicts, Observed occurrences actually in it.
type Rate struct {
	Expected float64
	Observed float64
}

// IsChronic reports whether the alert was statistically going to appear in the
// window (Expected >= chronicExpectedMin) and is not bursting past its own baseline
// (Observed <= chronicBurstFactor*Expected).
func (r Rate) IsChronic() bool {
	return r.Expected >= chronicExpectedMin && r.Observed <= chronicBurstFactor*r.Expected
}

// SubjectKey collapses an alert's stored subject to the entity a same-incident
// group keys on: the owning workload (owner reference, else the hash-stripped
// name). It is the single read-side identity function — the replay harness routes
// through it too, so one normalization rule is scored. Lower-cased and trimmed to
// match the legacy impactKey, so mixed-case webhook subjects don't fragment.
func SubjectKey(a AlertIdentity) string {
	ns := strings.ToLower(strings.TrimSpace(a.SubjectNamespace))
	// Datastore alerts carry a logical database name, not a workload; all datnames on
	// one server share a sibling set. Staged for the datastore-grouping follow-up —
	// production leaves Series empty, so this branch is inert there.
	if strings.EqualFold(a.SubjectType, "database") && len(a.Series) > 0 {
		s := make([]string, len(a.Series))
		for i, v := range a.Series {
			s[i] = strings.ToLower(strings.TrimSpace(v))
		}
		sort.Strings(s)
		return "db|" + ns + "|" + strings.Join(s, ",")
	}
	subj := strings.ToLower(strings.TrimSpace(a.SubjectOwner))
	if subj == "" {
		subj = WorkloadName(a.SubjectName)
	}
	return ns + "|" + subj
}

// WorkloadName reduces a stored name to the workload it belongs to, by stripping a
// trailing ReplicaSet/pod-template hash ("product-reviews-76cf66f66b" ->
// "product-reviews"), then lower-casing and trimming.
//
// Exported because the knowledge graph reports the same workload under both forms and
// has to be reduced to the same identity the event side uses before the two can be
// matched — see api.scopeAndNormalize. Keeping one implementation means the read side
// cannot drift into two different answers for "which workload is this".
func WorkloadName(name string) string {
	return podTemplateHashSuffix.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "")
}

// AssembleTiers sorts each candidate into a tier relative to seed, returning
// candidate-ID -> tier. Candidates absent from the result are unrelated to the
// seed. dependsOn maps a SubjectKey to the upstream identities it calls; rates maps
// "SubjectKey|aggregation_key" to the trailing firing rate.
func AssembleTiers(seed AlertIdentity, candidates []AlertIdentity, dependsOn map[string][]string, rates map[string]Rate) map[string]string {
	sk := SubjectKey(seed)
	upstream := dependsOn[sk]
	downstream := reverseDependents(dependsOn)[sk]

	out := make(map[string]string, len(candidates))
	for _, e := range candidates {
		ek := SubjectKey(e)

		// Config change on the seed or an upstream, within the lead-in window: cause.
		// Deploys are never folded as chronic. Checked first so the seed's own deploy
		// is a cause, not swept into core by the same-subject rule below.
		if e.IsConfigChange {
			if (ek == sk || contains(upstream, ek)) &&
				e.TsOffsetS <= 0 && e.TsOffsetS >= CauseLeadInSeconds {
				out[e.ID] = TierCause
			}
			continue
		}

		// Same subject as the seed: the incident's own alerts. Never demoted to
		// chronic — you opened it, so a real incident on a noisy subject still shows.
		if ek == sk {
			out[e.ID] = TierCore
			continue
		}

		chronic := rates[rateKey(e)].IsChronic()

		// Derived signals (SLO / anomaly) are excluded from both causal lanes — see
		// isDerivedSignal. They are only ever core, on the seed's own subject.
		if isDerivedSignal(e) {
			continue
		}

		// Alert on an upstream dependency, in the cause window: candidate cause. The
		// window runs to CauseLagGraceSeconds past the root, not to the root itself —
		// see the constant. A candidate wired in BOTH directions keeps the strict bound
		// so the impact rule below still claims it: for a mutual pair timing is the only
		// separator, and extending the cause window would silently relabel impact as cause.
		if contains(upstream, ek) {
			lateBound := CauseLagGraceSeconds
			if contains(downstream, ek) {
				lateBound = 0
			}
			if e.TsOffsetS <= lateBound {
				out[e.ID] = foldIfChronic(TierCause, chronic)
				continue
			}
		}

		// Alert on a downstream dependent, at/after the root: candidate impact.
		if contains(downstream, ek) && e.TsOffsetS >= 0 {
			out[e.ID] = foldIfChronic(TierImpact, chronic)
			continue
		}
		// Otherwise unrelated to the seed: excluded.
	}
	return out
}

// Relation values describe how a candidate is wired to the seed in the service map.
// Pairs with edges in BOTH directions are common (two services that call each other);
// for those the cause-vs-impact split rests on timing alone, which is a weak signal —
// RelationMutual surfaces that instead of hiding it.
const (
	RelationSeedCalls = "seed_calls" // the seed calls this service (a dependency of the seed)
	RelationCallsSeed = "calls_seed" // this service calls the seed (a dependent)
	RelationMutual    = "mutual"     // edges exist in both directions
)

// RelationTo reports how cand is connected to seed. Empty when there is no direct
// edge either way.
func RelationTo(seed, cand AlertIdentity, dependsOn map[string][]string) string {
	sk, ck := SubjectKey(seed), SubjectKey(cand)
	seedCalls := contains(dependsOn[sk], ck)
	callsSeed := contains(dependsOn[ck], sk)
	switch {
	case seedCalls && callsSeed:
		return RelationMutual
	case seedCalls:
		return RelationSeedCalls
	case callsSeed:
		return RelationCallsSeed
	}
	return ""
}

func rateKey(a AlertIdentity) string { return SubjectKey(a) + "|" + a.AggregationKey }

// RateKey is the key AssembleTiers looks a candidate's Rate up under
// ("SubjectKey|aggregation_key"). A caller building the rates map must key it with
// this so the lookup inside AssembleTiers matches.
func RateKey(a AlertIdentity) string { return rateKey(a) }

func foldIfChronic(tier string, chronic bool) string {
	if chronic {
		return TierChronic
	}
	return tier
}

// reverseDependents inverts the depends-on map: identity -> identities that call it.
func reverseDependents(dependsOn map[string][]string) map[string][]string {
	rev := make(map[string][]string, len(dependsOn))
	for svc, ups := range dependsOn {
		for _, up := range ups {
			rev[up] = append(rev[up], svc)
		}
	}
	return rev
}

// contains is defined in classification.go (same package).
