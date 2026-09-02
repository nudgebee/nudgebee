package recommendation

import (
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
)

// pr_value_drift.go decides whether an already-open rightsizing pull request has
// fallen far enough behind its recommendation to be worth rewriting in place
// (#34959).
//
// Both sides of the comparison are produced by the same runbook-server rule code
// and stored on the resolution, so they are directly comparable — but they are
// not textually comparable: the same memory figure shows up variously as
// "223357747", "172247Ki" and "238Mi" depending on which run wrote it. Quantities
// are therefore parsed rather than string-matched.

// driftFields are the fields compared within each dimension. A pull request that
// sets only a request while the recommendation now also wants a limit counts as
// drifted — the shape changed, not just the number.
var driftFields = []string{"request", "limit"}

// valueDrift is one dimension that moved far enough to matter.
type valueDrift struct {
	Container string
	Dimension string // "cpu" or "memory"
	Field     string // "request" or "limit"
	Old       string // as the open pull request has it; empty when newly added
	New       string
	ChangePct float64 // 0 when Old was absent or zero — the change is unbounded
}

// String renders a drift the way it is shown to a human, on the pull request and
// in the auto optimize's own reason text.
func (d valueDrift) String() string {
	if d.Old == "" {
		return fmt.Sprintf("%s %s %s: now set to %s", d.Container, d.Dimension, d.Field, d.New)
	}
	return fmt.Sprintf("%s %s %s: %s → %s (%+.0f%%)", d.Container, d.Dimension, d.Field, d.Old, d.New, d.ChangePct)
}

// detectValueDrift reports the dimensions where freshly computed values have
// moved at least their configured percentage away from what the open pull request
// proposes.
//
// thresholds is keyed by dimension so cpu and memory keep their own trigger
// percentages, exactly as the auto optimize rule declares them; a dimension with
// no threshold is not compared at all. An empty result means the pull request is
// still close enough to leave alone.
//
// Only values the recommendation still wants are considered. A field the open
// pull request sets but the recommendation has dropped is deliberately ignored:
// rewriting a pull request to *remove* a limit is a different and riskier change
// than keeping its numbers current, and is not what this exists to do.
func detectValueDrift(oldValues, newValues map[string]any, thresholds map[string]float64) []valueDrift {
	if len(thresholds) == 0 || len(newValues) == 0 {
		return nil
	}

	var drifts []valueDrift

	containers := make([]string, 0, len(newValues))
	for container := range newValues {
		containers = append(containers, container)
	}
	// Stable ordering so the message a reviewer sees does not reshuffle between
	// runs for reasons that have nothing to do with the values.
	sort.Strings(containers)

	for _, container := range containers {
		newDims, ok := newValues[container].(map[string]any)
		if !ok {
			continue
		}
		oldDims, _ := oldValues[container].(map[string]any)

		dimensions := make([]string, 0, len(thresholds))
		for dimension := range thresholds {
			dimensions = append(dimensions, dimension)
		}
		sort.Strings(dimensions)

		for _, dimension := range dimensions {
			newFields, ok := newDims[dimension].(map[string]any)
			if !ok {
				continue
			}
			oldFields, _ := oldDims[dimension].(map[string]any)

			for _, field := range driftFields {
				newQty, newRaw, ok := quantityOf(newFields, field)
				if !ok {
					continue // the recommendation no longer asks for this field
				}

				oldQty, oldRaw, hadOld := quantityOf(oldFields, field)
				if !hadOld {
					// The open pull request does not touch this at all — for example
					// its payload carries only memory while the recommendation now
					// also wants a cpu request. Treat as drifted: the pull request is
					// materially incomplete, not merely slightly stale.
					drifts = append(drifts, valueDrift{
						Container: container, Dimension: dimension, Field: field, New: newRaw,
					})
					continue
				}

				changePct, comparable := percentChange(oldQty, newQty)
				if !comparable {
					// Old value was zero: any non-zero new value is a real change, and
					// a percentage of it is meaningless.
					if newQty != 0 {
						drifts = append(drifts, valueDrift{
							Container: container, Dimension: dimension, Field: field,
							Old: oldRaw, New: newRaw,
						})
					}
					continue
				}

				if abs(changePct) >= thresholds[dimension] {
					drifts = append(drifts, valueDrift{
						Container: container, Dimension: dimension, Field: field,
						Old: oldRaw, New: newRaw, ChangePct: changePct,
					})
				}
			}
		}
	}

	return drifts
}

// quantityOf parses one field out of a dimension payload, returning the numeric
// value, the original text (for display), and whether a usable value was present.
func quantityOf(fields map[string]any, field string) (float64, string, bool) {
	if fields == nil {
		return 0, "", false
	}
	raw, present := fields[field]
	if !present || raw == nil {
		return 0, "", false
	}
	text := strings.TrimSpace(fmt.Sprintf("%v", raw))
	if text == "" || text == "<nil>" {
		return 0, "", false
	}

	// Some payloads carry thousands separators ("4,000Mi"), which ParseQuantity
	// rejects. Strip them rather than losing the comparison to a formatting choice.
	parsed, err := resource.ParseQuantity(strings.ReplaceAll(text, ",", ""))
	if err != nil {
		return 0, "", false
	}
	return parsed.AsApproximateFloat64(), text, true
}

// percentChange returns the signed percentage from old to new. comparable is
// false when old is zero, where a percentage has no meaning.
func percentChange(oldVal, newVal float64) (float64, bool) {
	if oldVal == 0 {
		return 0, false
	}
	return (newVal - oldVal) / oldVal * 100, true
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// describeDrifts renders drifts as a single line for a status message or log.
func describeDrifts(drifts []valueDrift) string {
	parts := make([]string, 0, len(drifts))
	for _, d := range drifts {
		parts = append(parts, d.String())
	}
	return strings.Join(parts, "; ")
}
