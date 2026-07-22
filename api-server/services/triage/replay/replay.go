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
	"regexp"
	"sort"
	"strings"
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
	// Tier is the incident tier the event belongs to (core | chronic). Recorded
	// for future per-tier scoring; the pairwise score below is tier-agnostic.
	Tier string `json:"tier"`

	// Future fields (Phase 3, #34658): scoring cross-service cause/impact grouping
	// — where members of one incident have different subjects (a deploy wave, an
	// upstream failure) — will need per-event timing and change markers, e.g.
	// `StartsAt time.Time` and `IsConfigChange bool`. Add them here and to the
	// golden set when the deterministic assembly is scored, so the schema grows
	// deliberately rather than being retrofitted.
}

// GoldenSet is a labelled corpus of events.
type GoldenSet struct {
	Name   string        `json:"name"`
	Events []GoldenEvent `json:"events"`
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

// hashSuffix matches a trailing ReplicaSet/pod-template hash (e.g. "-5cbd8ddb97",
// "-6b9kfmn4p2"). Kubernetes encodes these with a vowel-free base32 alphabet
// (k8s.io/apimachinery rand.SafeEncodeString: "bcdfghjklmnpqrstvwxz" + digits),
// so match that rather than [a-f0-9] (which misses the many hashes containing
// g/h/j/k/m/n/... ) or [a-z0-9] (which would wrongly strip real trailing words
// like "-server"/"-worker" that contain vowels). This regex models the DB-based
// owner resolution the real ingestion path does; it is only the fallback used
// here when a golden event has no recorded owner.
var hashSuffix = regexp.MustCompile(`-[bcdfghjklmnpqrstvwxz0-9]{6,10}$`)

// NormalizedGrouper models the intended grouping: prefer the owning workload,
// strip a ReplicaSet/pod hash suffix, and collapse per-datname datastore alerts
// onto their shared server signal (the sorted sibling-datname set). It groups by
// subject identity — the root entity — not by alert type, so all symptoms on one
// subject form one incident.
type NormalizedGrouper struct{}

func (NormalizedGrouper) Name() string { return "normalized" }

func (NormalizedGrouper) Key(e GoldenEvent) string {
	// Datastore alerts carry a logical database name, not a workload. All datnames
	// on one server report a shared sibling set, so key on that instead of the
	// per-datname name.
	if strings.EqualFold(e.SubjectType, "database") && len(e.Series) > 0 {
		s := append([]string(nil), e.Series...)
		sort.Strings(s)
		return "db|" + e.SubjectNamespace + "|" + strings.Join(s, ",")
	}
	subj := e.SubjectOwner
	if subj == "" {
		subj = hashSuffix.ReplaceAllString(e.SubjectName, "")
	}
	return e.SubjectNamespace + "|" + subj
}

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
