package integrations

import (
	"fmt"
	"sort"
	"strings"

	"nudgebee/services/integrations/core"
)

// azureCommonAlertSchemaID is the schemaId Azure Monitor stamps on every
// common-alert-schema body, whatever the signal type.
const azureCommonAlertSchemaID = "azureMonitorCommonAlertSchema"

// Dimension names come from whatever the alert rule splits by — the columns of a
// log-alert query, or a metric's dimensions — so they are free text. These sets
// cover the names Container Insights and the AKS platform metrics use; any other
// dimension still passes through as a plain lowercased label.
var (
	azureNamespaceDimensions  = map[string]bool{"namespace": true, "kubernetes namespace": true, "k8s_namespace": true}
	azurePodDimensions        = map[string]bool{"pod": true, "podname": true, "pod_name": true, "pod name": true}
	azureControllerDimensions = map[string]bool{"controllername": true, "controller_name": true, "controller name": true}
)

// azureCommonSchemaToLabels flattens an Azure Monitor common-schema alert into the
// label vocabulary the webhook parsers already consume: the rule becomes
// alertname, the split-by dimensions become subject_name/subject_namespace for
// resolveSubjectFromLabels, and nb_alert_fingerprint gets a key that stays the
// same each time the same rule fires for the same dimensions.
//
// essentials.alertId is deliberately not part of that key: Azure mints a new one
// for every alert instance, so keying on it means no firing ever repeats.
//
// Returns an empty map when the body names no rule, so the caller treats it as
// "no labels recovered" exactly like an empty Alertmanager payload.
func azureCommonSchemaToLabels(a AzureMonitorAlert) map[string]string {
	labels := map[string]string{}
	ess := a.Data.Essentials
	if ess.AlertRule == "" {
		return labels
	}

	labels["alertname"] = ess.AlertRule
	labels["nb_alert_source"] = "azure"
	setIfNotEmpty(labels, "nb_alert_severity", ess.Severity)
	setIfNotEmpty(labels, "monitor_condition", ess.MonitorCondition)
	setIfNotEmpty(labels, "signal_type", ess.SignalType)
	setIfNotEmpty(labels, "azure_alert_rule_id", ess.AlertRuleID)

	target := ""
	if len(ess.AlertTargetIDs) > 0 {
		target = ess.AlertTargetIDs[0]
		labels["azure_alert_target_resource"] = target
	}

	var fingerprintDims []string
	var pod, controller string
	for _, d := range azureConditionDimensions(a.Data.AlertContext.Condition) {
		key := strings.ToLower(strings.TrimSpace(d.name))
		value := strings.TrimSpace(d.value)
		if key == "" || value == "" {
			continue
		}
		switch {
		case azureNamespaceDimensions[key]:
			labels["namespace"] = value
			labels["subject_namespace"] = value
		case azurePodDimensions[key]:
			pod = value
		case azureControllerDimensions[key]:
			controller = value
			// A Deployment's pods report their ReplicaSet here, and every rollout
			// mints a new one. Key the fingerprint on the Deployment so a firing
			// after a rollout still joins the same occurrence chain.
			value = core.TrimPodTemplateHash(value)
		}
		labelKey := strings.ReplaceAll(key, " ", "_")
		if _, taken := labels[labelKey]; !taken {
			labels[labelKey] = strings.TrimSpace(d.value)
		}
		fingerprintDims = append(fingerprintDims, key+"="+value)
	}

	// The workload is a better subject than one of its pods: it survives pod
	// churn and is what the inventory matcher looks up. The raw ReplicaSet name
	// rides along as a second candidate in case the hash trim was wrong.
	switch {
	case controller != "":
		workload := core.TrimPodTemplateHash(controller)
		labels["subject_name"] = workload
		if workload != controller {
			labels[core.WorkloadCandidatesLabel] = controller
		}
	case pod != "":
		labels["subject_name"] = pod
	}

	ruleKey := ess.AlertRuleID
	if ruleKey == "" {
		ruleKey = ess.AlertRule
	}
	sort.Strings(fingerprintDims)
	labels["nb_alert_fingerprint"] = core.CanonicalFingerprint(
		append([]string{"azure", strings.ToLower(ruleKey), strings.ToLower(target)}, fingerprintDims...)...)

	return labels
}

type azureDimension struct {
	name  string
	value string
}

// azureConditionDimensions collects the {name, value} dimension pairs from every
// criterion. They arrive untyped ([]any of JSON objects) because metric and log
// alerts share the field.
func azureConditionDimensions(c AzureMonitorCondition) []azureDimension {
	var out []azureDimension
	for _, crit := range c.AllOf {
		for _, raw := range crit.Dimensions {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			if name == "" {
				continue
			}
			value := ""
			if v, present := m["value"]; present && v != nil {
				value = fmt.Sprint(v)
			}
			out = append(out, azureDimension{name: name, value: value})
		}
	}
	return out
}
