package scan_orchestrator

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// KubeProxyVersionRuleName matches the collector's RuleName.KUBE_PROXY_VERSION
// (event_handler.py:338 / upgrade_handler.py:20). UI keys on this exact value.
const KubeProxyVersionRuleName = "kube_proxy_version"

const (
	// versionDifferenceThreshold mirrors collector's
	// upgrade_handler.py:40 — diff ≥ 3 → severity High; else Low.
	versionDifferenceThreshold = 3
	// maxSkewThresholdVersion mirrors collector's upgrade_handler.py:41 —
	// K8s v1.25+ allows kube-proxy 3-minor skew; older 2-minor.
	maxSkewThresholdVersion = 25
)

// k8sVersionRegex matches a "1.NN.PP" or "v1.NN.PP" version string. The
// agent's get_resource snake-cases keys but values are passed through
// untouched, so kube-proxy container images keep their original "v1.28.5"
// shape.
var k8sVersionRegex = regexp.MustCompile(`v?(\d+\.\d+\.\d+)`)

// k8sVersionMinorRegex pulls just the minor (`1.34.6` → `34`) out of a
// version string. Used for both kube-proxy and node kubelet_version.
var k8sVersionMinorRegex = regexp.MustCompile(`v?\d+\.(\d+)`)

// ParseKubeProxyVersionRecommendations runs the version-skew math the
// collector did in upgrade_handler.py::generate_kube_proxy_version_recommendations
// (lines 467-601). One Recommendation row when there's a real skew; nothing
// when versions match.
//
// kubeletVersion: any node's status.node_info.kubelet_version
// (e.g. "v1.34.6-gke.1154000"). Used as a proxy for the control plane version
// since the K8s /version endpoint isn't exposed via get_resource.
//
// kubeProxyVersion: parsed from a kube-proxy pod's container.image string
// (e.g. extracted "1.28.5"). Empty string → no kube-proxy found, return [].
//
// account_object_id matches the collector's
// f"{kp_version}-{current_version}-{target_version}" format so existing rows
// UPSERT in place after the migration.
func ParseKubeProxyVersionRecommendations(kubeProxyVersion, kubeletVersion string, account ScanAccount) ([]Recommendation, error) {
	if kubeProxyVersion == "" || kubeletVersion == "" {
		return nil, nil
	}
	kpMinor, err := parseMinorVersion(kubeProxyVersion)
	if err != nil {
		return nil, fmt.Errorf("kube_proxy_version: parse kube-proxy version %q: %w", kubeProxyVersion, err)
	}
	cpMinor, err := parseMinorVersion(kubeletVersion)
	if err != nil {
		return nil, fmt.Errorf("kube_proxy_version: parse kubelet version %q: %w", kubeletVersion, err)
	}

	// Two skew checks: against current control plane and against (current+1)
	// target. Combine messages and pick the higher severity.
	currentMaxSkew := maxSkewForVersion(cpMinor)
	currentMsg, currentSev := versionSkewMessage(kpMinor, cpMinor, currentMaxSkew, kubeProxyVersion, "current")

	targetVersion := cpMinor + 1
	targetMaxSkew := maxSkewForVersion(targetVersion)
	targetMsg, targetSev := versionSkewMessage(kpMinor, targetVersion, targetMaxSkew, kubeProxyVersion, "target")

	combinedMsg := strings.TrimSpace(currentMsg + "\n" + targetMsg)
	if combinedMsg == "" {
		return nil, nil
	}
	severity := combineSeverities(currentSev, targetSev)

	// Only emit a row when at least one skew check produced a non-Info
	// message. The collector's `if message: generate_recommendation` check
	// passed even on Info messages (because the message string was non-empty
	// for matching versions too). We mirror that — a "matches" row gets
	// emitted with severity Info, which the UI may filter out.
	recommendationContent := map[string]any{
		"kube-proxy":          kubeProxyVersion,
		"current_k8s_version": cpMinor,
		"target_k8s_version":  targetVersion,
		"message":             combinedMsg,
	}
	recJSON, err := json.Marshal(recommendationContent)
	if err != nil {
		return nil, fmt.Errorf("kube_proxy_version: encode recommendation: %w", err)
	}

	return []Recommendation{{
		CloudAccountID:       account.AccountID,
		TenantID:             account.TenantID,
		Category:             "InfraUpgrade",
		RuleName:             KubeProxyVersionRuleName,
		RecommendationAction: "Modify",
		Recommendation:       string(recJSON),
		Severity:             severity,
		Status:               "Open",
		AccountObjectID:      fmt.Sprintf("%s-%d-%d", kubeProxyVersion, cpMinor, targetVersion),
	}}, nil
}

// parseMinorVersion extracts the minor (`v1.34.6` → 34) from a K8s-style
// version string. Mirrors collector's _parse_kube_proxy_version
// (upgrade_handler.py:467-480) and the inline regex in
// k8s_version_upgrade.py:106 (`re.sub(r"\D", "", version_info.minor)`).
func parseMinorVersion(s string) (int, error) {
	m := k8sVersionMinorRegex.FindStringSubmatch(s)
	if len(m) < 2 {
		return 0, fmt.Errorf("no minor version in %q", s)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("invalid minor version %q: %w", m[1], err)
	}
	return n, nil
}

// versionSkewMessage mirrors the collector's check_version_skew
// (upgrade_handler.py:387-437). Returns (message, severity).
func versionSkewMessage(kpMinor, cpMinor, maxSkew int, kpVersion, versionType string) (string, string) {
	diff := kpMinor - cpMinor
	if diff < 0 {
		diff = -diff
	}
	// Same — no skew. Return Info message so the caller still emits a row
	// (matches collector behaviour where matching versions still wrote a row).
	if kpMinor == cpMinor {
		return fmt.Sprintf("Kube-proxy version %s matches your %s control plane version %d. ", kpVersion, versionType, cpMinor), "Info"
	}

	severity := severityForVersionDiff(diff)

	switch {
	case kpMinor > cpMinor:
		return fmt.Sprintf("kube-proxy v%s is newer than %s v1.%d. ", kpVersion, versionType, cpMinor), severity
	case kpMinor < cpMinor-maxSkew:
		return fmt.Sprintf("kube-proxy v%s is too old for %s control plane version v1.%d (must be within %d minor versions). ", kpVersion, versionType, cpMinor, maxSkew), severity
	default:
		return fmt.Sprintf("kube-proxy v%s is behind your %s control plane version v1.%d.Although it is supported and allowed, please review the version skew. ", kpVersion, versionType, cpMinor), "Medium"
	}
}

func severityForVersionDiff(diff int) string {
	if diff < versionDifferenceThreshold {
		return "Low"
	}
	return "High"
}

func maxSkewForVersion(version int) int {
	if version >= maxSkewThresholdVersion {
		return 3
	}
	return 2
}

func combineSeverities(a, b string) string {
	if a == "High" || b == "High" {
		return "High"
	}
	if a == "Medium" || b == "Medium" {
		return "Medium"
	}
	if a == "Low" || b == "Low" {
		return "Low"
	}
	return "Info"
}

// extractKubeProxyVersion finds the kube-proxy container's image among a
// list of pods (typically in kube-system) and parses out the version.
// Returns "" when no kube-proxy pod is found — typical on managed clusters
// (e.g. GKE Dataplane V2 / EKS with anetd) where kube-proxy is replaced by
// eBPF or a different binary. Caller emits no recommendation in that case.
//
// Defensive against pathological JSON input: nil pods, missing spec/containers,
// non-map container entries. Go's nil-map indexing is safe, but the explicit
// guards make the data shape contract obvious to a reader.
func extractKubeProxyVersion(pods []map[string]any) string {
	for _, p := range pods {
		if p == nil {
			continue
		}
		spec, ok := p["spec"].(map[string]any)
		if !ok || spec == nil {
			continue
		}
		containers, ok := spec["containers"].([]any)
		if !ok {
			continue
		}
		for _, c := range containers {
			cm, ok := c.(map[string]any)
			if !ok || cm == nil {
				continue
			}
			image, _ := cm["image"].(string)
			if !strings.Contains(image, "kube-proxy") {
				continue
			}
			m := k8sVersionRegex.FindStringSubmatch(image)
			if len(m) >= 2 {
				return m[1]
			}
		}
	}
	return ""
}

// extractKubeletVersion returns any node's status.node_info.kubelet_version
// — used as a proxy for the K8s control plane version. Picks the first
// non-empty one. Same defensive shape as extractKubeProxyVersion.
func extractKubeletVersion(nodes []map[string]any) string {
	for _, n := range nodes {
		if n == nil {
			continue
		}
		status, ok := n["status"].(map[string]any)
		if !ok || status == nil {
			continue
		}
		ni, ok := status["node_info"].(map[string]any)
		if !ok || ni == nil {
			continue
		}
		if v, _ := ni["kubelet_version"].(string); v != "" {
			return v
		}
	}
	return ""
}
