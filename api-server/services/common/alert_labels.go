package common

// AlertPodLabel keeps the pod an alert named. Workload matching replaces the
// event's "pod" label with the owning workload's name, which no metric series
// carries, so the alert's own series could not be told from the rule's other
// breaching objects.
const AlertPodLabel = "nb_alert_pod"

// SeriesMatchLabels returns the event labels to compare a metric series' labels
// against: the event's labels with "pod" set back to the pod the alert named,
// when that was kept. Every reader that picks "the series this event fired for"
// goes through it, so they agree on the object. The input is not modified.
func SeriesMatchLabels[V any](labels map[string]V) map[string]V {
	pod, ok := labels[AlertPodLabel]
	if !ok {
		return labels
	}
	if name, isString := any(pod).(string); !isString || name == "" {
		return labels
	}
	out := make(map[string]V, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	out["pod"] = pod
	return out
}
