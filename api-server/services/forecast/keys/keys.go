// Package keys names the forecast alerts (events.aggregation_key). It imports nothing,
// so the packages that act on these alerts (event resolution, triage) can share one
// list with the forecast package, which imports them: forecast itself cannot be
// imported back without a cycle.
package keys

import "strings"

const (
	CPUThrottlingPredicted      = "CPUThrottlingPredicted"
	CPUSaturationPredicted      = "CPUSaturationPredicted"
	CPUThrottlingSLOBreached    = "CPUThrottlingSLOBreached"
	CPUSaturationSLOBreached    = "CPUSaturationSLOBreached"
	MemorySaturationPredicted   = "MemorySaturationPredicted"
	MemorySaturationSLOBreached = "MemorySaturationSLOBreached"
)

// All is every forecast alert key.
func All() []string {
	return []string{
		CPUThrottlingPredicted, CPUSaturationPredicted, CPUThrottlingSLOBreached, CPUSaturationSLOBreached,
		MemorySaturationPredicted, MemorySaturationSLOBreached,
	}
}

// Memory is the memory forecast's alert keys.
func Memory() []string {
	return []string{MemorySaturationPredicted, MemorySaturationSLOBreached}
}

// IsForecast reports whether key is a forecast alert key, ignoring case: triage
// compares lowercased finding types.
func IsForecast(key string) bool {
	for _, k := range All() {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}
