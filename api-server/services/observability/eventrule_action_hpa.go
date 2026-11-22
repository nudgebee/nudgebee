package observability

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/security"
)

// hpa_mismatch_enricher answers what a KubeHpaMaxedOut / KubeHpaReplicasMismatch
// alert states but never shows: the HPA is pinned, or its replica count does not
// match what it asked for — at what numbers, against which metric, and whether
// Kubernetes says it is able to scale at all.
//
// Today these alerts resolve their subject to the scaled WORKLOAD, so the
// investigation carries that deployment's events and charts and not one figure
// about the autoscaler. The alert is about the HPA; the evidence was about the
// thing it scales.
//
// The action name already existed as an `agent_playbook_action` catalog row and
// an `agent_playbook` binding for KubeHpaReplicasMismatch, with no
// implementation behind it — the same dead-name situation as the over-commitment
// enrichers. Registering under that exact name turns the existing row live with
// no migration.
func init() {
	playbooks.RegisterAction("hpa_mismatch_enricher", &hpaStatusAction{})
}

// hpaAlertKeys are the autoscaler alerts this card answers. Both are statements
// about the HPA object itself, which is why the trigger is the alert rather than
// a subject shape — the subject resolves to the scaled workload.
var hpaAlertKeys = map[string]bool{
	"KubeHpaMaxedOut":          true,
	"KubeHpaReplicasMismatch":  true,
	"KubeHpaMaxedOutV2":        true,
	"KubernetesHpaScaleCapped": true,
}

const hpaLookbackMinutes = 10

type hpaStatusAction struct{}

func (a *hpaStatusAction) CanAutoExecute(ctx playbooks.PlaybookActionContext) bool {
	if !hpaAlertKeys[ctx.GetEvent().AggregationKey] {
		return false
	}
	name, _ := hpaNameNamespace(ctx.GetEvent())
	return name != ""
}

func (a *hpaStatusAction) AutoExecute(ctx playbooks.PlaybookActionContext) (playbooks.PlaybookActionResponse, error) {
	name, namespace := hpaNameNamespace(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"hpa_name": name, "namespace": namespace})
}

// hpaNameNamespace reads the autoscaler's identity from the alert's own labels.
// kube-state-metrics emits `horizontalpodautoscaler`; some relabelings shorten it
// to `hpa`. The subject is NOT used as a fallback: on these alerts it is the
// scaled workload, so treating it as the HPA name would query a non-existent
// autoscaler and render an empty card that looks authoritative.
func hpaNameNamespace(event playbooks.PlaybookEvent) (string, string) {
	if event.Labels == nil {
		return "", ""
	}
	name := ""
	for _, key := range []string{"horizontalpodautoscaler", "hpa"} {
		if v := strings.TrimSpace(event.Labels[key]); v != "" {
			name = v
			break
		}
	}
	if name == "" {
		return "", ""
	}
	namespace := strings.TrimSpace(event.Labels["namespace"])
	if namespace == "" {
		namespace = strings.TrimSpace(event.SubjectNamespace)
	}
	return name, namespace
}

func (a *hpaStatusAction) Execute(ctx playbooks.PlaybookActionContext, rawParams map[string]any) (playbooks.PlaybookActionResponse, error) {
	hpaName, _ := rawParams["hpa_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	if hpaName == "" {
		return nil, errors.New("hpa_mismatch_enricher: hpa_name required")
	}
	tenantID, accountID := ctx.GetTenantId(), ctx.GetAccountId()
	if tenantID == "" || accountID == "" {
		return nil, errors.New("hpa_mismatch_enricher: tenant ID and account ID required")
	}
	requestCtx := security.NewRequestContextForTenantAdmin(tenantID, ctx.GetLogger(), nil, nil)

	provider, _, provErr := GetLogsMetricsTracesProvider(requestCtx, accountID, "", "metrics", "")
	if provErr != nil {
		return nil, fmt.Errorf("hpa_mismatch_enricher: metrics provider: %w", provErr)
	}
	if provider != integrationPrometheus {
		return nil, fmt.Errorf("hpa_mismatch_enricher: needs prometheus metrics, account uses %q", provider)
	}

	start, end := playbooks.RangeQueryWindow(ctx.GetEvent(), hpaLookbackMinutes, time.Now().UTC())
	output, err := FetchMetricsQuery(requestCtx, FetchMetricsRequest{
		AccountId:      accountID,
		MetricProvider: integrationPrometheus,
		Queries:        hpaQueries(hpaName, namespace),
		StartTime:      start.UnixMilli(),
		EndTime:        end.UnixMilli(),
		StepInterval:   60,
	})
	if err != nil {
		return nil, fmt.Errorf("hpa_mismatch_enricher: prom: %w", err)
	}
	results := latestValuesByKey(output)

	current, haveCurrent := firstValue(results["current_replicas"])
	maxReplicas, haveMax := firstValue(results["max_replicas"])
	if !haveCurrent && !haveMax {
		// Without replica counts there is no card worth drawing, and an empty
		// one reads as "the autoscaler is fine".
		return nil, errors.New("hpa_mismatch_enricher: kube-state-metrics returned no HPA series")
	}
	desired, haveDesired := firstValue(results["desired_replicas"])
	minReplicas, haveMin := firstValue(results["min_replicas"])

	rows := [][]any{{"Autoscaler", hpaLabel(namespace, hpaName)}}
	if haveCurrent {
		rows = append(rows, []any{"Running replicas", formatCount(current)})
	}
	if haveDesired {
		rows = append(rows, []any{"Replicas it wants", formatCount(desired)})
	}
	if haveMin || haveMax {
		rows = append(rows, []any{"Allowed range", hpaRange(minReplicas, haveMin, maxReplicas, haveMax)})
	}
	if haveCurrent && haveMax && current >= maxReplicas {
		rows = append(rows, []any{"At its ceiling", "Yes — cannot add replicas"})
	}

	// The metric rows are the actionable half: they say WHY it wants more.
	metricRows, pinnedMetric := hpaMetricRows(results)
	rows = append(rows, metricRows...)

	// Only surface conditions that are a problem. "AbleToScale=true" on every
	// card is noise; "AbleToScale=false" is the whole answer.
	rows = append(rows, hpaConditionRows(results["conditions"])...)

	return playbooks.PlaybookActionResponseTable{
		Rows:    rows,
		Headers: []string{"Measure", "Value"},
		AdditionalInfo: map[string]any{
			"title":              "Autoscaler status",
			"action_name":        "hpa_mismatch_enricher",
			"actual_action_name": "hpa_mismatch_enricher",
			"hpa_name":           hpaName,
			"namespace":          namespace,
		},
		Insight: hpaInsight(hpaName, namespace, current, haveCurrent, maxReplicas, haveMax, pinnedMetric),
	}, nil
}

// hpaQueries builds the kube-state-metrics reads, scoped to this autoscaler.
// Split out from Execute so the scoping is testable without a metrics provider.
func hpaQueries(hpaName, namespace string) map[string]string {
	selector := fmt.Sprintf(`horizontalpodautoscaler=%q`, hpaName)
	if namespace != "" {
		selector += fmt.Sprintf(`, namespace=%q`, namespace)
	}
	return map[string]string{
		"current_replicas": fmt.Sprintf(`kube_horizontalpodautoscaler_status_current_replicas{%s}`, selector),
		"desired_replicas": fmt.Sprintf(`kube_horizontalpodautoscaler_status_desired_replicas{%s}`, selector),
		"min_replicas":     fmt.Sprintf(`kube_horizontalpodautoscaler_spec_min_replicas{%s}`, selector),
		"max_replicas":     fmt.Sprintf(`kube_horizontalpodautoscaler_spec_max_replicas{%s}`, selector),
		"target_metric":    fmt.Sprintf(`kube_horizontalpodautoscaler_spec_target_metric{%s}`, selector),
		"current_metric":   fmt.Sprintf(`kube_horizontalpodautoscaler_status_target_metric{%s}`, selector),
		// Only the conditions that currently hold; a condition at 0 is not news.
		"conditions": fmt.Sprintf(`kube_horizontalpodautoscaler_status_condition{%s} == 1`, selector),
	}
}

// hpaMetricRows pairs each metric the HPA scales on with its current reading, so
// the card says "cpu utilization 93% against a 70% target" rather than leaving
// the reader to guess which signal pinned it.
//
// kube-state-metrics emits the current reading once per metric_target_type
// (average, utilization, value) whatever the target's type is, so a reading is
// only comparable to a target of the same type: pairing a utilization target with
// the `value` series rendered 0.2 cores as "0%" and 91328512 bytes as "91328512%".
// Readings of a type no target uses are dropped; a metric with no target at all
// keeps every reading, each labelled with its own type.
func hpaMetricRows(results map[string][]latestSample) ([][]any, string) {
	type metricKey struct{ name, targetType string }
	targets := map[metricKey]float64{}
	targetNames := map[string]bool{}
	for _, s := range results["target_metric"] {
		key := metricKey{s.Metric["metric_name"], s.Metric["metric_target_type"]}
		if key.name == "" || !usableValue(s.Value) {
			continue
		}
		targets[key] = s.Value
		targetNames[key.name] = true
	}
	currents := map[metricKey]float64{}
	for _, s := range results["current_metric"] {
		key := metricKey{s.Metric["metric_name"], s.Metric["metric_target_type"]}
		if key.name != "" && usableValue(s.Value) {
			currents[key] = s.Value
		}
	}

	keys := make([]metricKey, 0, len(targets))
	for key := range targets {
		keys = append(keys, key)
	}
	for key := range currents {
		if _, seen := targets[key]; !seen && !targetNames[key.name] {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].name != keys[j].name {
			return keys[i].name < keys[j].name
		}
		return keys[i].targetType < keys[j].targetType
	})

	rows := make([][]any, 0, len(keys))
	pinned := ""
	for _, key := range keys {
		target, hasTarget := targets[key]
		current, hasCurrent := currents[key]
		label := key.name
		if key.targetType != "" {
			label = key.name + " " + key.targetType
		}
		switch {
		case hasCurrent && hasTarget:
			rows = append(rows, []any{label, fmt.Sprintf("%s (target %s)", hpaMetricValue(current, key.targetType), hpaMetricValue(target, key.targetType))})
			if current > target && pinned == "" {
				pinned = label
			}
		case hasTarget:
			rows = append(rows, []any{label, "target " + hpaMetricValue(target, key.targetType)})
		case hasCurrent:
			rows = append(rows, []any{label, hpaMetricValue(current, key.targetType)})
		}
	}
	return rows, pinned
}

// hpaConditionRows surfaces only the conditions that block scaling. A healthy
// autoscaler adds no rows.
func hpaConditionRows(samples []latestSample) [][]any {
	type cond struct{ name, status string }
	seen := make([]cond, 0, len(samples))
	for _, s := range samples {
		name, status := s.Metric["condition"], s.Metric["status"]
		if name == "" || status == "" {
			continue
		}
		switch name {
		case "AbleToScale", "ScalingActive":
			// These are problems only when false.
			if strings.EqualFold(status, "true") {
				continue
			}
		case "ScalingLimited":
			// This one is a problem when true — it is Kubernetes saying the
			// autoscaler wanted to move and was not allowed to.
			if !strings.EqualFold(status, "true") {
				continue
			}
		default:
			continue
		}
		seen = append(seen, cond{name: name, status: status})
	}
	sort.Slice(seen, func(i, j int) bool { return seen[i].name < seen[j].name })

	rows := make([][]any, 0, len(seen))
	for _, c := range seen {
		rows = append(rows, []any{c.name, c.status + hpaConditionNote(c.name)})
	}
	return rows
}

func hpaConditionNote(condition string) string {
	switch condition {
	case "ScalingLimited":
		return " — held at a replica bound"
	case "AbleToScale":
		return " — Kubernetes cannot scale this workload"
	case "ScalingActive":
		return " — the metric could not be read"
	}
	return ""
}

func hpaInsight(hpaName, namespace string, current float64, haveCurrent bool, maxReplicas float64, haveMax bool, pinnedMetric string) []playbooks.PlaybookActionResponseInsight {
	if !haveCurrent || !haveMax || current < maxReplicas {
		return []playbooks.PlaybookActionResponseInsight{}
	}
	msg := fmt.Sprintf("%s is at its maximum of %s replicas and cannot add more.",
		hpaLabel(namespace, hpaName), formatCount(maxReplicas))
	if pinnedMetric != "" {
		msg += fmt.Sprintf(" %s is above its target, so demand is still unserved.", pinnedMetric)
	}
	return []playbooks.PlaybookActionResponseInsight{{Message: msg, Severity: "warning"}}
}

func hpaLabel(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}

func hpaRange(minReplicas float64, haveMin bool, maxReplicas float64, haveMax bool) string {
	switch {
	case haveMin && haveMax:
		return fmt.Sprintf("min %s, max %s", formatCount(minReplicas), formatCount(maxReplicas))
	case haveMax:
		return "max " + formatCount(maxReplicas)
	default:
		return "min " + formatCount(minReplicas)
	}
}

// hpaMetricValue renders a target/observed value. kube-state-metrics reports a
// utilization target as a plain percentage; everything else is a raw quantity
// whose unit we do not know, so it is left unannotated rather than mislabelled.
func hpaMetricValue(v float64, targetType string) string {
	if !usableValue(v) {
		// int64(NaN) is undefined in Go, and "NaN%" in a card is worse than
		// saying plainly that the reading is missing.
		return "unknown"
	}
	if strings.EqualFold(targetType, "utilization") {
		return fmt.Sprintf("%.0f%%", v)
	}
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.2f", v)
}

// usableValue rejects NaN and Inf. Prometheus renders both as real samples, and
// every comparison against NaN is false in Go — so an unguarded NaN slips past
// `current < maxReplicas` and makes hpaInsight announce that an autoscaler is at
// its ceiling when in truth we have no reading at all. Absent data must read as
// absent, never as a confident number.
func usableValue(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// firstValue reports the single value of a per-HPA series, and whether there was
// one at all — distinguishing "zero replicas" from "no data", which matter
// differently on this card. A NaN/Inf sample counts as no data.
func firstValue(samples []latestSample) (float64, bool) {
	for _, s := range samples {
		if !usableValue(s.Value) {
			continue
		}
		return s.Value, true
	}
	return 0, false
}
