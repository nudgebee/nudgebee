package integrations

import (
	"nudgebee/services/event"
	"strings"
)

// MapAlertStatus maps Grafana/Prometheus lowercase status to EventStatus constants.
func MapAlertStatus(status string) event.EventStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "firing":
		return event.EventStatusFiring
	case "resolved":
		return event.EventStatusResolved
	case "closed":
		return event.EventStatusClosed
	default:
		return event.EventStatusFiring
	}
}

// MapAlertSeverity maps Grafana/Prometheus lowercase severity to EventPriortiy constants.
func MapAlertSeverity(severity string) event.EventPriortiy {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "critical":
		return event.EventPriortiyHigh
	case "high":
		return event.EventPriortiyHigh
	case "warning", "warn", "medium":
		return event.EventPriortiyMedium
	case "low":
		return event.EventPriortiyLow
	case "info", "informational", "none":
		return event.EventPriortiyInfo
	case "debug":
		return event.EventPriortiyDebug
	default:
		return event.EventPriortiyLow
	}
}

// MapStringAnyToStringString safely converts map[string]any to map[string]string,
// dropping non-string values.
func MapStringAnyToStringString(input any) map[string]string {
	result := map[string]string{}
	if input == nil {
		return result
	}
	if casted, ok := input.(map[string]any); ok {
		for k, v := range casted {
			if s, ok := v.(string); ok {
				result[k] = s
			}
		}
	}
	return result
}

// ExtractK8sSubject picks the K8s workload subject from alert labels.
// Priority: pod > deployment > statefulset > daemonset > replicaset > job > cronjob.
func ExtractK8sSubject(labels map[string]string) (kind, name string) {
	for _, k := range []string{"pod", "deployment", "statefulset", "daemonset", "replicaset", "job", "cronjob"} {
		if v := labels[k]; v != "" {
			return k, v
		}
	}
	return "", ""
}
