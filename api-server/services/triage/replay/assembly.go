package replay

import "nudgebee/services/triage"

// Seed-relative assembly scoring (#34658, Step 3). The flat Grouper in replay.go
// partitions events by a single key — right for the "same incident" tier, but it
// cannot express cause/impact, which are *directional and relative to the alert a
// viewer opened*: the same alert is impact of one incident and core of another.
// This scores that.
//
// The IntendedAssembler delegates to the production triage.AssembleTiers, so this
// gate measures the real tiering logic (no reference-vs-production drift). The
// BaselineAssembler models today's raw same-name grouping, present so the scorer's
// discrimination is visible. ScoreAssembly reports per-tier recall against
// hand-labelled incidents plus how many in-window distractors were pulled in.

// TierNone is a golden-only label: an in-window distractor that must be excluded.
const TierNone = "none"

// toIdentity projects a labelled golden event onto the production AlertIdentity —
// the meaning-free signals an assembler is allowed to read.
func toIdentity(e GoldenEvent) triage.AlertIdentity {
	return triage.AlertIdentity{
		ID:               e.ID,
		SubjectType:      e.SubjectType,
		SubjectName:      e.SubjectName,
		SubjectNamespace: e.SubjectNamespace,
		SubjectOwner:     e.SubjectOwner,
		Series:           e.Series,
		AggregationKey:   e.AggregationKey,
		TsOffsetS:        e.TsOffsetS,
		IsConfigChange:   e.IsConfigChange,
		FindingType:      e.FindingType,
	}
}

// AssemblyResult maps event ID -> tier assigned relative to the seed's incident.
// Events absent from the map are judged unrelated to the seed (excluded).
type AssemblyResult struct {
	Tiers map[string]string
}

// Assembler sorts a window of events into tiers relative to the opened (seed)
// event. The scorer blanks the ground-truth labels on the events it passes, so an
// assembler can only read the meaning-free signals plus the fixture topology and
// rates — the same inputs the production path derives from the DB and the KG.
type Assembler interface {
	Name() string
	Assemble(seed GoldenEvent, events []GoldenEvent, dependsOn map[string][]string, rates map[string]triage.Rate) AssemblyResult
}

// BaselineAssembler models today's behaviour: it groups only alerts sharing the raw
// stored subject name, and knows nothing of causes, impact, or noise. It recovers
// same-name core members and scores zero on every cross-service tier.
type BaselineAssembler struct{}

func (BaselineAssembler) Name() string { return "baseline" }

func (BaselineAssembler) Assemble(seed GoldenEvent, events []GoldenEvent, _ map[string][]string, _ map[string]triage.Rate) AssemblyResult {
	out := AssemblyResult{Tiers: map[string]string{}}
	rawSeed := seed.SubjectNamespace + "|" + seed.SubjectName
	for _, e := range events {
		if e.SubjectNamespace+"|"+e.SubjectName == rawSeed {
			out.Tiers[e.ID] = triage.TierCore
		}
	}
	return out
}

// IntendedAssembler delegates to the production four-tier assembler, so the gate
// scores production logic directly.
type IntendedAssembler struct{}

func (IntendedAssembler) Name() string { return "intended" }

func (IntendedAssembler) Assemble(seed GoldenEvent, events []GoldenEvent, dependsOn map[string][]string, rates map[string]triage.Rate) AssemblyResult {
	cands := make([]triage.AlertIdentity, len(events))
	for i, e := range events {
		cands[i] = toIdentity(e)
	}
	return AssemblyResult{Tiers: triage.AssembleTiers(toIdentity(seed), cands, dependsOn, rates)}
}

// AssemblyScore holds a seed-relative assembler's per-tier recall against the
// labelled incidents, plus how many in-window distractors it wrongly pulled in.
type AssemblyScore struct {
	Assembler       string
	CoreRecall      float64
	CauseRecall     float64
	ImpactRecall    float64
	ChronicFoldRate float64 // chronic-labelled members correctly folded (never shown as related)
	FalseIncludes   int     // "none"-labelled distractors placed into any tier

	CoreHit, CoreTot       int
	CauseHit, CauseTot     int
	ImpactHit, ImpactTot   int
	ChronicHit, ChronicTot int
	ExcludeTot             int
}

// ScoreAssembly runs the assembler once per labelled incident (seeded on that
// incident's Seed event, over that incident's own events) and tallies per-tier
// recall plus false inclusions of "none" distractors. Each incident is scored in
// isolation because TsOffsetS is relative to that incident's root; mixing incidents
// would make the offsets meaningless. Cross-incident over-merge is instead probed
// with explicit "none" distractors inside an incident's window.
func ScoreAssembly(set GoldenSet, a Assembler) AssemblyScore {
	byIncident := map[string][]GoldenEvent{}
	order := []string{}
	for _, e := range set.Events {
		if _, seen := byIncident[e.Incident]; !seen {
			order = append(order, e.Incident)
		}
		byIncident[e.Incident] = append(byIncident[e.Incident], e)
	}

	sc := AssemblyScore{Assembler: a.Name()}
	for _, incident := range order {
		members := byIncident[incident]
		seed, ok := findSeed(members)
		if !ok {
			continue // not an assembly-labelled incident (e.g. the flat-corpus rows)
		}
		res := a.Assemble(seed, stripLabels(members), set.DependsOn, set.Rates)
		for _, e := range members {
			got := res.Tiers[e.ID]
			switch e.Tier {
			case triage.TierCore:
				sc.CoreTot++
				if got == triage.TierCore {
					sc.CoreHit++
				}
			case triage.TierCause:
				sc.CauseTot++
				if got == triage.TierCause {
					sc.CauseHit++
				}
			case triage.TierImpact:
				sc.ImpactTot++
				if got == triage.TierImpact {
					sc.ImpactHit++
				}
			case triage.TierChronic:
				sc.ChronicTot++
				if got == triage.TierChronic {
					sc.ChronicHit++
				}
			case TierNone:
				sc.ExcludeTot++
				if got != "" {
					sc.FalseIncludes++
				}
			}
		}
	}
	sc.CoreRecall = ratio(sc.CoreHit, sc.CoreTot)
	sc.CauseRecall = ratio(sc.CauseHit, sc.CauseTot)
	sc.ImpactRecall = ratio(sc.ImpactHit, sc.ImpactTot)
	sc.ChronicFoldRate = ratio(sc.ChronicHit, sc.ChronicTot)
	return sc
}

func findSeed(members []GoldenEvent) (GoldenEvent, bool) {
	for _, e := range members {
		if e.Seed {
			return e, true
		}
	}
	return GoldenEvent{}, false
}

// stripLabels blanks the ground-truth fields so an assembler cannot read the
// answer; it keeps only the signals the production path would have.
func stripLabels(members []GoldenEvent) []GoldenEvent {
	out := make([]GoldenEvent, len(members))
	for i, e := range members {
		e.Incident = ""
		e.Tier = ""
		e.Seed = false
		out[i] = e
	}
	return out
}

// ratio is recall with the vacuous convention that an empty denominator scores 1.0
// (an assembler is not penalised for a tier the corpus does not exercise).
func ratio(hit, tot int) float64 {
	if tot == 0 {
		return 1.0
	}
	return float64(hit) / float64(tot)
}
