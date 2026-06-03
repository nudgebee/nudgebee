package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Popeye output (popeye -A -o json --force-exit-zero) shape (v0.21.5):
//
//	{
//	  "popeye": {
//	    "report_time": "2026-05-08T17:32:00Z",
//	    "score": 76,
//	    "grade": "C",
//	    "errors": [],
//	    "sections": [
//	      {
//	        "linter":  "deployments",
//	        "gvr":     "apps/v1/deployments",
//	        "tally":   {"ok":..,"info":..,"warning":..,"error":..,"score":..},
//	        "issues": {
//	          "namespace/name": [
//	            {"group":"__root__","gvr":"...","level":2,"message":"[POP-100] No probes defined"},
//	            ...
//	          ],
//	          "cluster-scoped-name": [...]
//	        }
//	      },
//	      ...
//	    ]
//	  }
//	}
//
// Each `issues` map key is `namespace/name` for namespaced resources and just
// `name` for cluster-scoped ones (clusterroles, clusterrolebindings, nodes, …).
//
// The collector's handle_popeye_report joined service_keys against
// k8s_pods/k8s_workloads to map each row to a resource_id. PR-C-1 ships the
// structural parser without that DB enrichment — every row becomes a
// "general" recommendation keyed on namespace/Kind/name. The DB-side mapping
// lands in the cron handler PR alongside upsert/archive logic.

type popeyeReport struct {
	Popeye struct {
		Score    float64         `json:"score"`
		Grade    string          `json:"grade"`
		Sections []popeyeSection `json:"sections"`
	} `json:"popeye"`
}

type popeyeSection struct {
	Linter string                   `json:"linter"`
	GVR    string                   `json:"gvr"`
	Issues map[string][]popeyeIssue `json:"issues"`
}

type popeyeIssue struct {
	Group   string `json:"group"`
	GVR     string `json:"gvr"`
	Level   int    `json:"level"`
	Message string `json:"message"`
}

var popeyeCodePattern = regexp.MustCompile(`\[POP-(\d+)\]`)

// PopeyeRuleNameLabel is what we register the popeye scanner under in
// ScannerCatalog. Used purely for archive scoping in Persist (the per-row
// rule_name comes from popeyeRuleNameForLinter — the collector emits
// "<linter>_misconfigurations" per resource type, e.g.
// `deployments_misconfigurations`, `clusterroles_misconfigurations`).
const PopeyeRuleNameLabel = "popeye_misconfigurations"

// popeyeRuleNameForLinter mirrors the collector's per-linter rule_name shape
// at event_handler.py:1814 — `<linter>_misconfigurations` where linter is
// popeye's lowercase plural ("deployments", "pods", "clusterroles", ...). The
// UI keys on these literal values; renaming the helper or the suffix is a
// UI-visible break.
func popeyeRuleNameForLinter(linter string) string {
	if linter == "" {
		return "misconfigurations"
	}
	return linter + "_misconfigurations"
}

// ParsePopeye turns popeye's stdout JSON into Recommendation rows.
//
// One row per (section, resource_key) — i.e. one Recommendation per real
// K8s resource that has at least one level>0 issue. Issues with level==0
// (Ok) are dropped. Severity follows the collector's mapping (priority
// 1=Info, 2=Medium, 3=Critical) using the maximum level across the row's
// issues.
func ParsePopeye(stdout string, account ScanAccount) ([]Recommendation, error) {
	var report popeyeReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return nil, fmt.Errorf("popeye: parse json: %w", err)
	}
	out := make([]Recommendation, 0)
	for _, section := range report.Popeye.Sections {
		kind := normalizePopeyeKind(section.Linter)
		for resourceKey, issues := range section.Issues {
			namespace, name := splitResourceKey(resourceKey)
			insights := make([]map[string]any, 0, len(issues))
			maxLevel := 0
			for _, issue := range issues {
				if issue.Level == 0 {
					continue
				}
				if issue.Level > maxLevel {
					maxLevel = issue.Level
				}
				code, message := splitPopeyeCode(issue.Message)
				insights = append(insights, map[string]any{
					"namespace": namespace,
					"kind":      kind,
					"name":      name,
					"message":   message,
					"code":      code,
					"level":     issue.Level,
					"gvr":       section.GVR,
				})
			}
			if len(insights) == 0 {
				continue
			}
			recommendationJSON, err := json.Marshal(insights)
			if err != nil {
				return nil, fmt.Errorf("popeye: encode recommendation: %w", err)
			}
			out = append(out, Recommendation{
				CloudAccountID:       account.AccountID,
				TenantID:             account.TenantID,
				Category:             "Configuration",
				RuleName:             popeyeRuleNameForLinter(section.Linter),
				RecommendationAction: "Modify",
				Recommendation:       string(recommendationJSON),
				Severity:             popeyeSeverity(maxLevel),
				Status:               "Open",
				AccountObjectID:      formatAccountObjectID(namespace, kind, name),
			})
		}
	}
	return out, nil
}

// PopeyeScore extracts the cluster score from popeye's stdout. The cron
// handler can persist this into cloud_account_score (parity with
// handle_popeye_score) — kept as a separate accessor so it stays out of the
// per-resource Recommendation rows.
func PopeyeScore(stdout string) (float64, error) {
	var report popeyeReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return 0, fmt.Errorf("popeye: parse json: %w", err)
	}
	return report.Popeye.Score, nil
}

func popeyeSeverity(level int) string {
	switch level {
	case 1:
		return "Info"
	case 2:
		return "Medium"
	case 3:
		return "Critical"
	default:
		return "Info"
	}
}

// splitPopeyeCode pulls "[POP-100]" out of "[POP-100] No probes defined".
// Returns (code, cleanMessage). Mirrors get_message_code_message in the
// collector (event_handler.py:1863-1871).
func splitPopeyeCode(message string) (string, string) {
	m := popeyeCodePattern.FindStringSubmatch(message)
	if len(m) < 2 {
		return "", strings.TrimSpace(message)
	}
	cleaned := popeyeCodePattern.ReplaceAllString(message, "")
	return m[1], strings.TrimSpace(cleaned)
}

// splitResourceKey parses popeye's `namespace/name` (namespaced) or `name`
// (cluster-scoped) resource key. Returns (namespace, name); namespace is "" for
// cluster-scoped resources.
func splitResourceKey(key string) (namespace, name string) {
	if idx := strings.Index(key, "/"); idx >= 0 {
		return key[:idx], key[idx+1:]
	}
	return "", key
}

// formatAccountObjectID builds the legacy "namespace/Kind/name" service-key
// the collector's recommendation upsert path keys on. For cluster-scoped
// resources we emit "<empty>/Kind/name" — same as Robusta wrote them.
func formatAccountObjectID(namespace, kind, name string) string {
	return fmt.Sprintf("%s/%s/%s", namespace, kind, name)
}

// normalizePopeyeKind maps popeye's plural lowercase linter to the K8s
// capitalized singular ("deployments" → "Deployment"). Mirrors get_kind in
// the collector (event_handler.py:1758-1767), expanded to cover the v0.21.5
// linter set that didn't exist when the collector parser was written
// (cluster, configmaps, secrets, ingresses, …).
func normalizePopeyeKind(kind string) string {
	switch kind {
	case "deployments":
		return "Deployment"
	case "statefulsets":
		return "StatefulSet"
	case "daemonsets":
		return "DaemonSet"
	case "pods":
		return "Pod"
	case "services":
		return "Service"
	case "configmaps":
		return "ConfigMap"
	case "secrets":
		return "Secret"
	case "ingresses":
		return "Ingress"
	case "namespaces":
		return "Namespace"
	case "nodes":
		return "Node"
	case "persistentvolumes":
		return "PersistentVolume"
	case "persistentvolumeclaims":
		return "PersistentVolumeClaim"
	case "horizontalpodautoscalers":
		return "HorizontalPodAutoscaler"
	case "poddisruptionbudgets":
		return "PodDisruptionBudget"
	case "networkpolicies":
		return "NetworkPolicy"
	case "serviceaccounts":
		return "ServiceAccount"
	case "clusterroles":
		return "ClusterRole"
	case "clusterrolebindings":
		return "ClusterRoleBinding"
	case "roles":
		return "Role"
	case "rolebindings":
		return "RoleBinding"
	case "replicasets":
		return "ReplicaSet"
	case "cronjobs":
		return "CronJob"
	case "jobs":
		return "Job"
	case "cluster":
		return "Cluster"
	}
	return kind
}
