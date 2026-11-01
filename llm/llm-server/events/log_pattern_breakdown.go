package events

import (
	"encoding/json"
)

// LogPatternBreakdownAction is the playbook action whose evidence card carries
// the error-log-spike breakdown.
const LogPatternBreakdownAction = "aggregated_alert_label_enricher"

// LogPatternBreakdown is which error log patterns rose for an error-log-spike
// alert, against what is normal for the workload. It mirrors the card data the
// api-server writes under "pattern_breakdown" (LogPatternBreakdown in
// api-server/services/observability/eventrule_action_log_pattern_breakdown.go,
// where each field is documented); a field renamed there must be renamed here.
type LogPatternBreakdown struct {
	WindowStart         string           `json:"window_start"`
	WindowEnd           string           `json:"window_end"`
	BucketMinutes       int              `json:"bucket_minutes"`
	BaselineHours       int              `json:"baseline_hours"`
	BaselineEvaluatedAt string           `json:"baseline_evaluated_at"`
	BaselineKnown       bool             `json:"baseline_known"`
	CountedLevels       []string         `json:"counted_levels"`
	CountingNote        string           `json:"counting_note"`
	Total               LogPatternTotal  `json:"total"`
	PatternsSeen        int              `json:"patterns_seen"`
	Patterns            []LogPatternRise `json:"patterns"`
}

type LogPatternTotal struct {
	Peak   float64 `json:"peak"`
	PeakAt string  `json:"peak_at"`
	Normal float64 `json:"normal"`
}

type LogPatternRise struct {
	PatternHash string  `json:"pattern_hash"`
	Level       string  `json:"level"`
	Peak        float64 `json:"peak"`
	PeakAt      string  `json:"peak_at"`
	Normal      float64 `json:"normal"`
	Example     string  `json:"example"`
	ContainerID string  `json:"container_id"`
}

// DecodeLogPatternBreakdown reads the breakdown out of the card's data. The
// card holds it under "pattern_breakdown"; data may be the decoded object or
// the JSON text of it. Returns false when the card has no breakdown — the
// action also runs for alerts it only enriches labels for — or the data is not
// readable as one.
func DecodeLogPatternBreakdown(data any) (LogPatternBreakdown, bool) {
	var raw []byte
	switch v := data.(type) {
	case nil:
		return LogPatternBreakdown{}, false
	case string:
		raw = []byte(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return LogPatternBreakdown{}, false
		}
		raw = b
	}
	var card struct {
		PatternBreakdown *LogPatternBreakdown `json:"pattern_breakdown"`
	}
	if err := json.Unmarshal(raw, &card); err != nil || card.PatternBreakdown == nil {
		return LogPatternBreakdown{}, false
	}
	if len(card.PatternBreakdown.Patterns) == 0 {
		return LogPatternBreakdown{}, false
	}
	return *card.PatternBreakdown, true
}
