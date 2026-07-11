package k8s

import (
	"context"
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/types"
	"nudgebee/runbook/services/relay"
	"nudgebee/runbook/services/security"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// In-place pod resize (KEP-1287) requirements:
//   - cluster (kube-apiserver/kubelet) >= 1.35, where the feature reached GA /
//     Stable. We deliberately do NOT enable it on the 1.33/1.34 beta: beta
//     support was uneven across managed providers (e.g. EKS), so we wait for
//     the stable release. Memory-limit decrease is also only permitted from GA,
//     so gating on 1.35 covers it without a separate check.
//   - agent kubectl client >= 1.32 (for `--subresource=resize`)
//
// The cluster version is read from the agent table's k8s_version column. The
// agent's kubectl client version is a Nudgebee-controlled deployment guarantee
// (the agent image ships kubectl >= 1.32); if an old agent ever lacks it, the
// resize patch fails and we fall back to the rollout path, so it is not gated
// here.
var (
	inPlaceMinServerMajor, inPlaceMinServerMinor = 1, 35
)

// kubeVersion is a parsed major.minor Kubernetes version.
type kubeVersion struct {
	major int
	minor int
}

func (v kubeVersion) atLeast(major, minor int) bool {
	if v.major != major {
		return v.major > major
	}
	return v.minor >= minor
}

func (v kubeVersion) String() string { return fmt.Sprintf("%d.%d", v.major, v.minor) }

// kubeVersionRegex extracts the leading major.minor from a version string such
// as "v1.33.2", "v1.33.11-eks-40737a8", "v1.35.3-gke.1389002", or "1.34.0+".
var kubeVersionRegex = regexp.MustCompile(`^v?(\d+)\.(\d+)`)

func parseKubeVersion(version string) (kubeVersion, error) {
	m := kubeVersionRegex.FindStringSubmatch(strings.TrimSpace(version))
	if m == nil {
		return kubeVersion{}, fmt.Errorf("unrecognized kubernetes version %q", version)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return kubeVersion{major: major, minor: minor}, nil
}

// getClusterK8sVersion reads the cluster version for an account's k8s agent
// from the agent table (k8s_version column, e.g. "v1.33.11-eks-40737a8").
func getClusterK8sVersion(ctx context.Context, accountId string) (kubeVersion, error) {
	// Fail fast on an empty account: an empty cloud_account_id would scan the
	// whole agent table cross-tenant. Callers pass the task's account id.
	if accountId == "" {
		return kubeVersion{}, fmt.Errorf("accountId is empty")
	}
	dbm, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return kubeVersion{}, err
	}
	var raw string
	err = dbm.Db.GetContext(ctx, &raw, `
		SELECT k8s_version FROM agent
		WHERE cloud_account_id = $1 AND type = 'k8s'
		  AND k8s_version IS NOT NULL AND k8s_version <> ''
		ORDER BY (status = 'CONNECTED') DESC, updated_at DESC
		LIMIT 1`, accountId)
	if err != nil {
		return kubeVersion{}, fmt.Errorf("no k8s_version found for account %s: %w", accountId, err)
	}
	return parseKubeVersion(raw)
}

// isInPlaceResizableKind reports whether the workload kind owns pods whose
// resources can be resized in place. Jobs/CronJobs are excluded (short-lived,
// init-container heavy); bare Pods are handled directly.
func isInPlaceResizableKind(kind string) bool {
	switch strings.ToLower(kind) {
	case "deployment", "statefulset", "replicaset", "daemonset", "pod":
		return true
	default:
		return false
	}
}

// inPlaceDecision captures whether to attempt an in-place resize and, if not, why.
type inPlaceDecision struct {
	eligible bool
	reason   string
}

// evaluateInPlace decides whether the direct-apply path should attempt an
// in-place resize. It is only called after GitOps/ticket handling has been
// skipped. Gating on cluster >= 1.35 (GA) means memory-limit decrease is also
// permitted, so no separate check is needed. QoS-class violations (which the
// kubelet rejects) are intentionally NOT pre-checked here — they surface as a
// patch error and trigger the recreate fallback, keeping the gate simple.
func evaluateInPlace(taskCtx types.TaskContext, accountId, kind string) inPlaceDecision {
	if !isInPlaceResizableKind(kind) {
		return inPlaceDecision{eligible: false, reason: fmt.Sprintf("kind %q is not in-place resizable", kind)}
	}

	server, err := getClusterK8sVersion(taskCtx.GetContext(), accountId)
	if err != nil {
		return inPlaceDecision{eligible: false, reason: fmt.Sprintf("cluster version lookup failed: %v", err)}
	}
	if !server.atLeast(inPlaceMinServerMajor, inPlaceMinServerMinor) {
		return inPlaceDecision{eligible: false, reason: fmt.Sprintf("cluster %s < %d.%d (in-place resize GA)", server.String(), inPlaceMinServerMajor, inPlaceMinServerMinor)}
	}
	return inPlaceDecision{eligible: true}
}

// listWorkloadPods returns the live pod objects selected by a workload's
// spec.selector.matchLabels. For kind=Pod it returns the single named pod.
func listWorkloadPods(requestContext *security.RequestContext, accountId, kind, name, namespace string) ([]map[string]any, error) {
	if strings.ToLower(kind) == "pod" {
		cmd := fmt.Sprintf("kubectl get pod %s -n %s -o json", name, namespace)
		resp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", cmd, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch pod '%s/%s': %w", namespace, name, err)
		}
		podMap, err := parseKubectlResponse(resp)
		if err != nil {
			return nil, err
		}
		return []map[string]any{podMap}, nil
	}

	getWorkloadCmd := fmt.Sprintf("kubectl get %s %s -n %s -o json", kind, name, namespace)
	workloadResp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", getWorkloadCmd, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch workload '%s/%s': %w", namespace, name, err)
	}
	workloadMap, err := parseKubectlResponse(workloadResp)
	if err != nil {
		return nil, err
	}
	labels, found, err := unstructured.NestedStringMap(workloadMap, "spec", "selector", "matchLabels")
	if !found || err != nil || len(labels) == 0 {
		return nil, fmt.Errorf("could not find selector labels for workload '%s/%s'", namespace, name)
	}
	selector := make([]string, 0, len(labels))
	for k, v := range labels {
		selector = append(selector, fmt.Sprintf("%s=%s", k, v))
	}
	getPodsCmd := fmt.Sprintf("kubectl get pods -l %s -n %s -o json", strings.Join(selector, ","), namespace)
	podsResp, err := relay.ExecuteRelayJob(requestContext, accountId, relay.RelayJobKubectl, "", getPodsCmd, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch pods for workload '%s/%s': %w", namespace, name, err)
	}
	podsMap, err := parseKubectlResponse(podsResp)
	if err != nil {
		return nil, err
	}
	items, found, err := unstructured.NestedSlice(podsMap, "items")
	if !found || err != nil {
		return nil, fmt.Errorf("no pods found for workload '%s/%s'", namespace, name)
	}
	pods := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if pm, ok := it.(map[string]any); ok {
			pods = append(pods, pm)
		}
	}
	return pods, nil
}

// resizeState is the outcome of inspecting a pod's resize status conditions.
type resizeState int

const (
	resizeDone resizeState = iota
	resizeInProgress
	resizeDeferred
	resizeInfeasible
)

// resizeStatus inspects a pod's status.conditions for the KEP-1287 resize
// conditions. When neither PodResizePending nor PodResizeInProgress is present,
// the resize is considered complete.
func resizeStatus(podMap map[string]any) (resizeState, string) {
	conditions, found, _ := unstructured.NestedSlice(podMap, "status", "conditions")
	if !found {
		return resizeDone, ""
	}
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok {
			continue
		}
		ctype, _ := cond["type"].(string)
		cstatus, _ := cond["status"].(string)
		if cstatus != "True" {
			continue
		}
		reason, _ := cond["reason"].(string)
		message, _ := cond["message"].(string)
		switch ctype {
		case "PodResizePending":
			if strings.EqualFold(reason, "Infeasible") {
				return resizeInfeasible, message
			}
			return resizeDeferred, message
		case "PodResizeInProgress":
			if strings.EqualFold(reason, "Error") {
				return resizeInfeasible, message
			}
			return resizeInProgress, message
		}
	}
	return resizeDone, ""
}
