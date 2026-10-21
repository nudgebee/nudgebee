// Package forecast projects when a workload metric will cross a threshold.
//
// It is a threshold-crossing forecast, not an anomaly model: it fits the trend of one
// metric over the last days (with its daily cycle taken out) and says when, if ever,
// the trend reaches the threshold. The metric is one of a fixed set of presets (see
// Presets); adding a metric is one entry there. One engine (Evaluate) serves the
// hourly SLO run for CPU SLOs, the on-demand forecast_generate RPC, and Nubi's
// metric_forecast tool through that RPC. See docs/cpu-throttling-forecast-spec.md.
package forecast

import (
	"sort"
	"time"

	"nudgebee/services/observability"
)

// Condition names a forecast preset. For the presets that are SLO types, it is also
// the slo_config.name of that type.
type Condition string

const (
	// ConditionThrottling is the throttled share of CFS periods, in percent.
	ConditionThrottling Condition = "cpu_throttling"
	// ConditionSaturation is CPU usage as a share of the CPU limit, in percent.
	ConditionSaturation Condition = "cpu_saturation"
)

// UnitCores is the unit of the CPU presets' limit and usage series. A preset over
// another resource names its own; memory is deferred until it can be measured per
// container (an OOM kill hits one container at its own limit, and a workload-wide
// ratio would dilute it).
const UnitCores = "cores"

// Preset is one forecastable metric. Every preset is a percentage: Value (or
// Value / Per when Per is set) times 100. Limit and Usage are workload totals of the
// resource the metric is measured against; the fit divides them by the pod count to
// cut the history at a limit change and to size a suggested limit.
type Preset struct {
	Condition Condition
	// Noun names the metric in titles and insights, e.g. "CPU throttling".
	Noun string
	// Resource names the limited resource in evidence, e.g. "CPU".
	Resource string
	// OfLimit says the threshold is a share of the limit ("75% of limit").
	OfLimit bool
	Value   observability.UtilisationKey
	Per     observability.UtilisationKey
	Limit   observability.UtilisationKey
	Usage   observability.UtilisationKey
	Unit    string
	// RoundStep is the granularity a suggested limit is rounded up to, in Unit.
	RoundStep        float64
	DefaultThreshold float64
	// SLO says the preset is also an SLO type: evaluated hourly, raising events.
	SLO bool
	// NoLimitReason is the reason given when the workload has no limit on Resource.
	NoLimitReason string
}

// Presets are the metrics the engine can forecast. 25% throttling is the
// kube-prometheus CPUThrottlingHigh bar.
var Presets = map[Condition]Preset{
	ConditionThrottling: {
		Condition: ConditionThrottling, Noun: "CPU throttling", Resource: "CPU",
		Value: observability.KeyCPUThrottlingRatio,
		Limit: observability.KeyCPULimit, Usage: observability.KeyCPUUsageLimited,
		Unit: UnitCores, RoundStep: 0.05, DefaultThreshold: 25, SLO: true,
		NoLimitReason: "the workload has no CPU limit, so it cannot be throttled",
	},
	ConditionSaturation: {
		Condition: ConditionSaturation, Noun: "CPU usage", Resource: "CPU", OfLimit: true,
		Value: observability.KeyCPUUsageLimited, Per: observability.KeyCPULimit,
		Limit: observability.KeyCPULimit, Usage: observability.KeyCPUUsageLimited,
		Unit: UnitCores, RoundStep: 0.05, DefaultThreshold: 90, SLO: true,
		NoLimitReason: "the workload has no CPU limit, so there is nothing to saturate",
	},
}

// PresetFor returns the preset of c, if there is one.
func PresetFor(c Condition) (Preset, bool) {
	p, ok := Presets[c]
	return p, ok
}

// IsCondition reports whether name is a forecastable metric.
func IsCondition(name string) bool {
	_, ok := Presets[Condition(name)]
	return ok
}

// IsSLOCondition reports whether name is a preset that is also an SLO type.
func IsSLOCondition(name string) bool {
	p, ok := Presets[Condition(name)]
	return ok && p.SLO
}

// ConditionNames lists the presets, sorted, for messages naming the valid choices.
func ConditionNames() []string {
	names := make([]string, 0, len(Presets))
	for c := range Presets {
		names = append(names, string(c))
	}
	sort.Strings(names)
	return names
}

// Status is the one-word answer of a forecast. Every non-projected answer says why,
// so "no warning" is never ambiguous between "safe" and "not checked".
type Status string

const (
	StatusProjected           Status = "projected"
	StatusNotProjected        Status = "not_projected"
	StatusBreached            Status = "breached"
	StatusInsufficientHistory Status = "insufficient_history"
	StatusNoLimit             Status = "no_limit"
	StatusProviderUnsupported Status = "provider_unsupported"
	StatusError               Status = "error"
)

// Level is the urgency of a projected breach.
type Level string

const (
	LevelHigh   Level = "HIGH"
	LevelMedium Level = "MEDIUM"
	// LevelBreached is the fingerprint level of the alert raised once the threshold is
	// actually crossed. It is not a Result.Level, which only grades projections.
	LevelBreached Level = "BREACHED"
)

// DailyRow is one day of the evidence table. Values are daily means of the hourly
// buckets; a projected row is the trend line at that day's midpoint. Limit and usage
// are per pod, in the Result's LimitUnit.
type DailyRow struct {
	Date        string  `json:"date"`
	ValuePct    float64 `json:"value_pct"`
	PerPodLimit float64 `json:"per_pod_limit,omitempty"`
	PerPodUsage float64 `json:"per_pod_usage,omitempty"`
	Projected   bool    `json:"projected,omitempty"`
}

// Result is the answer of one forecast. It is the response body of forecast_generate
// and the value stored in slo_config.last_forecast.
type Result struct {
	Status       Status    `json:"status"`
	Reason       string    `json:"reason,omitempty"`
	Condition    Condition `json:"condition"`
	ThresholdPct float64   `json:"threshold_pct"`

	Namespace string `json:"workload_namespace"`
	Workload  string `json:"workload_name"`
	Kind      string `json:"kind"`

	CurrentPct     float64    `json:"current_pct"`
	SlopePctPerDay float64    `json:"slope_pct_per_day"`
	ETAHours       *float64   `json:"eta_hours,omitempty"`
	ETAAt          *time.Time `json:"eta_at,omitempty"`
	Level          Level      `json:"level,omitempty"`

	HoursOfData    int     `json:"hours_of_data"`
	PerPodLimit    float64 `json:"per_pod_limit,omitempty"`
	SuggestedLimit float64 `json:"suggested_limit,omitempty"`
	// LimitUnit is the unit of every limit and usage figure, e.g. "cores".
	LimitUnit string `json:"limit_unit,omitempty"`

	Daily       []DailyRow `json:"daily"`
	Provider    string     `json:"provider,omitempty"`
	EvaluatedAt time.Time  `json:"evaluated_at"`
}
