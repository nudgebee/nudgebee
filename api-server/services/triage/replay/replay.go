// Package replay is the correlation replay harness (#34660): it scores how well
// an alert-grouping algorithm recovers hand-labelled real incidents, so a change
// to grouping can be measured with a number instead of hand-audited.
//
// The golden set (testdata/golden_audit.json) is a set of real recorded alerts
// from the correlation audit, each tagged with the incident a human says it
// belongs to. A Grouper assigns each event a key; events sharing a key form one
// group. Score compares the produced groups against the labels using pairwise
// precision/recall.
//
// The two reference groupers model today's behaviour (BaselineGrouper — raw
// subject, which fragments) vs the intended subject normalization
// (NormalizedGrouper — owner / hash-strip / shared datastore signal). Once the
// deterministic incident assembly (Phase 3, #34658) exists it plugs into the
// same Grouper interface and is scored against the same golden set.
package replay

import (
	"encoding/json"
	"os"

	"nudgebee/services/triage"
)

// GoldenEvent is one recorded alert with the fields grouping keys on, plus the
// incident a human labelled it with.
type GoldenEvent struct {
	ID               string `json:"id"`
	SubjectType      string `json:"subject_type"`
	SubjectName      string `json:"subject_name"`
	SubjectNamespace string `json:"subject_namespace"`
	SubjectOwner     string `json:"subject_owner"`
	AggregationKey   string `json:"aggregation_key"`
	// Series is the set of sibling datnames a datastore alert reported together
	// (Prometheus `_series`); all datnames on one server share it. Empty for
	// non-datastore alerts.
	Series []string `json:"series,omitempty"`
	// Incident is the human label: the incident (or chronic group) this event
	// belongs to. Events with the same Incident are one ground-truth group.
	Incident string `json:"incident"`
	// Tier is the incident tier the event belongs to (core | cause | impact |
	// chronic, or "none" for an in-window distractor that must be excluded). The
	// flat pairwise Score below is tier-agnostic; the seed-relative assembly scorer
	// (assembly.go) uses it.
	Tier string `json:"tier"`

	// TsOffsetS is the event's start time offset, in seconds, from its incident's
	// root (negative = before the root). The seed-relative assembly scorer orders
	// cause (before root) vs impact (at/after root) with it, without wall-clock time.
	TsOffsetS int `json:"ts_offset_s,omitempty"`
	// IsConfigChange marks a deploy / configuration-change event (cause-tier input),
	// mirroring events.finding_type = 'configuration_change'.
	IsConfigChange bool `json:"is_config_change,omitempty"`
	// FindingType is the raw events.finding_type ('issue', 'Anomaly', 'SLO', ...).
	// Derived signals (SLO/Anomaly) are kept out of the cause and impact lanes.
	FindingType string `json:"finding_type,omitempty"`
	// Seed marks the canonical event a viewer would open for this incident; the
	// seed-relative scorer assembles from it. Exactly one per assembly incident.
	// The flat pairwise corpus leaves it false.
	Seed bool `json:"seed,omitempty"`
}

// GoldenSet is a labelled corpus of events.
type GoldenSet struct {
	Name   string        `json:"name"`
	Events []GoldenEvent `json:"events"`

	// DependsOn maps a subject identity (SubjectKey form, "namespace|name") to the
	// upstream identities it calls. Fixture topology for the seed-relative assembly
	// scorer, standing in for the knowledge-graph walk the production path makes.
	// Empty for the flat pairwise corpus.
	DependsOn map[string][]string `json:"depends_on,omitempty"`
	// Rates maps "SubjectKey|aggregation_key" to the trailing firing rate the chronic
	// tier keys on, standing in for the per-(account,subject,aggregation_key) COUNT
	// the production path runs.
	Rates map[string]triage.Rate `json:"rates,omitempty"`
}

// LoadGoldenSet reads a labelled corpus from a JSON file.
func LoadGoldenSet(path string) (GoldenSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return GoldenSet{}, err
	}
	var set GoldenSet
	if err := json.Unmarshal(b, &set); err != nil {
		return GoldenSet{}, err
	}
	return set, nil
}

// Grouper assigns a grouping key to an event; events with equal keys form one
// group. Different grouping strategies implement this interface so the harness
// can score them against the same golden set.
type Grouper interface {
	Name() string
	Key(e GoldenEvent) string
}

// BaselineGrouper models today's behaviour: key on the raw stored subject name.
// ReplicaSet/pod-hash names and per-datname datastore alerts therefore fragment
// into many groups — the fragmentation the audit measured.
type BaselineGrouper struct{}

func (BaselineGrouper) Name() string { return "baseline" }

func (BaselineGrouper) Key(e GoldenEvent) string {
	return e.SubjectNamespace + "|" + e.SubjectName
}

// NormalizedGrouper models the intended grouping: prefer the owning workload,
// strip a ReplicaSet/pod hash suffix, and collapse per-datname datastore alerts
// onto their shared server signal (the sorted sibling-datname set). It groups by
// subject identity — the root entity — not by alert type, so all symptoms on one
// subject form one incident.
type NormalizedGrouper struct{}

func (NormalizedGrouper) Name() string { return "normalized" }

func (NormalizedGrouper) Key(e GoldenEvent) string { return SubjectKey(e) }

// SubjectKey delegates to the production identity function (triage.SubjectKey), so
// the harness scores the exact normalization the assembler uses — the reference
// model and production cannot drift (docs/incident-assembly-spec.md §5).
func SubjectKey(e GoldenEvent) string { return triage.SubjectKey(toIdentity(e)) }

// ScoreResult holds the pairwise precision/recall of a grouper against the labels.
type ScoreResult struct {
	Grouper   string
	Precision float64
	Recall    float64
	F1        float64
	TP        int // pairs the grouper and the labels agree belong together
	FP        int // pairs the grouper merged but the labels separate
	FN        int // pairs the labels group but the grouper split
	Groups    int // distinct groups the grouper produced
}

// Score computes pairwise precision/recall: over every pair of events, a pair is
// a true positive when the grouper and the human labels agree the two belong to
// the same incident. Precision penalises merging unrelated alerts; recall
// penalises fragmenting one incident.
func Score(set GoldenSet, g Grouper) ScoreResult {
	n := len(set.Events)
	keys := make([]string, n)
	distinct := map[string]struct{}{}
	for i, e := range set.Events {
		keys[i] = g.Key(e)
		distinct[keys[i]] = struct{}{}
	}
	var tp, fp, fn int
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			sameLabel := set.Events[i].Incident == set.Events[j].Incident
			sameGroup := keys[i] == keys[j]
			switch {
			case sameLabel && sameGroup:
				tp++
			case !sameLabel && sameGroup:
				fp++
			case sameLabel && !sameGroup:
				fn++
			}
		}
	}
	res := ScoreResult{Grouper: g.Name(), TP: tp, FP: fp, FN: fn, Groups: len(distinct)}
	// A grouper that merges nothing (all singletons) made no wrong merges, so it
	// is vacuously precise; only recall exposes that it grouped nothing. Reporting
	// 1.0 here keeps a future `precision >= X` gate from failing the safest grouper.
	res.Precision = 1.0
	if tp+fp > 0 {
		res.Precision = float64(tp) / float64(tp+fp)
	}
	if tp+fn > 0 {
		res.Recall = float64(tp) / float64(tp+fn)
	}
	if res.Precision+res.Recall > 0 {
		res.F1 = 2 * res.Precision * res.Recall / (res.Precision + res.Recall)
	}
	return res
}
