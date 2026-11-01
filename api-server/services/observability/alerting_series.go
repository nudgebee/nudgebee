package observability

import "sort"

// markAlertingSeries moves the series that belong to the object the alert fired
// for to the front of the rule's result and flags them "alerting": true.
//
// The rule's expression runs unchanged, so a rule that fires per object (per
// database, deployment, volume, node) returns every breaching object in no set
// order, and whichever came first is what the chart led with and the model
// quoted. The other series are kept: several objects breaching together matters.
//
// A series belongs to the alert when it agrees with the event on every label
// that tells the series apart — present on the event and valued differently
// across the result. Labels every series shares (job, the kube-state-metrics
// pod) say nothing about which object fired, and a drift in one of them must not
// unmark the real object. When no label tells the series apart, or every series
// agrees, nothing is marked: a mark on the wrong object is worse than none.
func markAlertingSeries(seriesList []any, eventLabels map[string]string) []any {
	if len(seriesList) < 2 || len(eventLabels) == 0 {
		return seriesList
	}

	metrics := make([]map[string]any, len(seriesList))
	for i, item := range seriesList {
		series, ok := item.(map[string]any)
		if !ok {
			return seriesList
		}
		metrics[i], _ = series["metric"].(map[string]any)
	}

	var keys []string
	for key, want := range eventLabels {
		if want == "" {
			continue
		}
		distinct := map[string]bool{}
		present := false
		for _, metric := range metrics {
			value, ok := metric[key].(string)
			present = present || ok
			distinct[value] = true
		}
		if present && len(distinct) > 1 {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return seriesList
	}

	alerting := make([]bool, len(seriesList))
	matched := 0
	for i, metric := range metrics {
		alerting[i] = true
		for _, key := range keys {
			if value, _ := metric[key].(string); value != eventLabels[key] {
				alerting[i] = false
				break
			}
		}
		if alerting[i] {
			matched++
		}
	}
	if matched == 0 || matched == len(seriesList) {
		return seriesList
	}

	order := make([]int, len(seriesList))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return alerting[order[a]] && !alerting[order[b]] })

	out := make([]any, 0, len(seriesList))
	for _, i := range order {
		series := seriesList[i].(map[string]any)
		if alerting[i] {
			series["alerting"] = true
		}
		out = append(out, series)
	}
	return out
}
