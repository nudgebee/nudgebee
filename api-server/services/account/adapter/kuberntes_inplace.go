package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"nudgebee/services/relay"
	"strings"
	"time"
)

// In-place rightsizing for the per-apply "Deploy Fix" path.
//
// The apply still inserts the existing `rightsizing_resource` agent_task, but
// when in-place is requested and the cluster is >= 1.35 it is created with
// status PROCESSING (in the legacy agent's IGNORE_STATUS) so the in-cluster
// agent skips it. api-server then resizes the running pods in place via the
// `resize` subresource and finalizes the task (COMPLETED), or — when in-place
// is infeasible/unsupported — flips the task back to TODO so the existing
// legacy rollout path applies it with a restart. This reuses the existing
// resolution tracking (GetRecommendationResolutionStatus reads agent_task) and
// the existing fallback, with no new resolution plumbing.

// inPlaceResizeContainer is the recommended resources for one container, as
// built by kuberntesAdapter.ApplyRecommendation (string quantities, e.g. cpu
// "0.5" and memory "663Mi").
type inPlaceResizeContainer struct {
	name          string
	cpuRequest    string
	cpuLimit      string
	memoryRequest string
	memoryLimit   string
}

func toResizeContainers(containers []any) []inPlaceResizeContainer {
	out := make([]inPlaceResizeContainer, 0, len(containers))
	for _, c := range containers {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, inPlaceResizeContainer{
			name:          asString(cm["container_name"]),
			cpuRequest:    asString(cm["cpu_request"]),
			cpuLimit:      asString(cm["cpu_limit"]),
			memoryRequest: asString(cm["memory_request"]),
			memoryLimit:   asString(cm["memory_limit"]),
		})
	}
	return out
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	s := fmt.Sprintf("%v", v)
	if s == "<nil>" {
		return ""
	}
	return s
}

// resizePatchBody builds the `{"spec":{"containers":[...]}}` body for the resize
// subresource. Only non-empty values are written (we set, never remove).
func resizePatchBody(containers []inPlaceResizeContainer) (string, error) {
	specContainers := make([]map[string]any, 0, len(containers))
	for _, c := range containers {
		if c.name == "" {
			continue
		}
		requests := map[string]any{}
		limits := map[string]any{}
		if c.cpuRequest != "" {
			requests["cpu"] = c.cpuRequest
		}
		if c.memoryRequest != "" {
			requests["memory"] = c.memoryRequest
		}
		if c.cpuLimit != "" {
			limits["cpu"] = c.cpuLimit
		}
		if c.memoryLimit != "" {
			limits["memory"] = c.memoryLimit
		}
		resources := map[string]any{}
		if len(requests) > 0 {
			resources["requests"] = requests
		}
		if len(limits) > 0 {
			resources["limits"] = limits
		}
		if len(resources) == 0 {
			continue
		}
		specContainers = append(specContainers, map[string]any{"name": c.name, "resources": resources})
	}
	if len(specContainers) == 0 {
		return "", fmt.Errorf("no container resources to resize")
	}
	patch := map[string]any{"spec": map[string]any{"containers": specContainers}}
	b, err := json.Marshal(patch)
	return string(b), err
}

// runKubectlAdapter runs a kubectl command in the customer cluster via the
// relay (kubectl_command_executor) and returns stdout/stderr.
func runKubectlAdapter(accountId, command string) (stdout, stderr string, err error) {
	resp, _, err := relay.ExecuteAndExtractResponse(relay.RelayExecuteRequest{
		Body: relay.ActionExecuteBody{
			AccountID:    accountId,
			ActionName:   "kubectl_command_executor",
			ActionParams: map[string]any{"command": command},
			Origin:       "services-server",
		},
		NoSinks: true,
	})
	if err != nil {
		return "", "", err
	}
	stdout, stderr = unwrapKubectlOutput(resp)
	return stdout, stderr, nil
}

// unwrapKubectlOutput recovers stdout/stderr from a kubectl_command_executor
// response (the agent wraps output in a JsonBlock under resp["data"]).
func unwrapKubectlOutput(resp map[string]any) (stdout, stderr string) {
	if s, ok := resp["stdout"].(string); ok && s != "" {
		stdout = s
	}
	if s, ok := resp["stderr"].(string); ok {
		stderr = s
	}
	if stdout != "" {
		return stdout, stderr
	}
	dataStr, ok := resp["data"].(string)
	if !ok || dataStr == "" {
		return stdout, stderr
	}
	var inner struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}
	if err := json.Unmarshal([]byte(dataStr), &inner); err != nil {
		return stdout, stderr
	}
	return inner.Stdout, inner.Stderr
}

// listWorkloadPodNames returns the pod names selected by a workload's
// spec.selector.matchLabels (or the single pod when kind is Pod).
func listWorkloadPodNames(accountId, kind, name, namespace string) ([]string, error) {
	if strings.EqualFold(kind, "pod") {
		return []string{name}, nil
	}
	stdout, stderr, err := runKubectlAdapter(accountId, fmt.Sprintf("kubectl get %s %s -n %s -o json", strings.ToLower(kind), name, namespace))
	if err != nil {
		return nil, err
	}
	if stdout == "" {
		return nil, fmt.Errorf("kubectl get %s failed: %s", kind, stderr)
	}
	var workload map[string]any
	if err := json.Unmarshal([]byte(stdout), &workload); err != nil {
		return nil, err
	}
	labels := nestedStringMap(workload, "spec", "selector", "matchLabels")
	if len(labels) == 0 {
		return nil, fmt.Errorf("no selector matchLabels for %s/%s", namespace, name)
	}
	sel := make([]string, 0, len(labels))
	for k, v := range labels {
		sel = append(sel, fmt.Sprintf("%s=%s", k, v))
	}
	stdout, stderr, err = runKubectlAdapter(accountId, fmt.Sprintf("kubectl get pods -l %s -n %s -o json", strings.Join(sel, ","), namespace))
	if err != nil {
		return nil, err
	}
	if stdout == "" {
		return nil, fmt.Errorf("kubectl get pods failed: %s", stderr)
	}
	var podList map[string]any
	if err := json.Unmarshal([]byte(stdout), &podList); err != nil {
		return nil, err
	}
	items, _ := podList["items"].([]any)
	names := make([]string, 0, len(items))
	for _, it := range items {
		if pm, ok := it.(map[string]any); ok {
			if n := nestedString(pm, "metadata", "name"); n != "" {
				names = append(names, n)
			}
		}
	}
	return names, nil
}

// patchPodResize patches one pod's resize subresource. rejected=true means the
// kubelet/apiserver refused the resize (QoS change, unsupported, etc.), so the
// caller should fall back to a rollout. A non-nil err is a transport failure.
func patchPodResize(accountId, pod, namespace, patchBody string) (rejected bool, err error) {
	_, stderr, err := runKubectlAdapter(accountId,
		fmt.Sprintf("kubectl patch pod %s -n %s --subresource=resize --patch '%s'", pod, namespace, patchBody))
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(stderr) != "" {
		return true, nil
	}
	return false, nil
}

// podResizeStatusOf fetches a pod and classifies its KEP-1287 resize status
// ("done"/"pending"/"inprogress"/"infeasible", or "unknown" on fetch error).
func podResizeStatusOf(accountId, pod, namespace string) string {
	stdout, _, err := runKubectlAdapter(accountId, fmt.Sprintf("kubectl get pod %s -n %s -o json", pod, namespace))
	if err != nil || stdout == "" {
		return "unknown"
	}
	var podMap map[string]any
	if json.Unmarshal([]byte(stdout), &podMap) != nil {
		return "unknown"
	}
	return podResizeState(podMap)
}

// podResizeState inspects status.conditions for the KEP-1287 resize conditions.
func podResizeState(podMap map[string]any) string {
	conditions, _ := nestedSlice(podMap, "status", "conditions")
	for _, c := range conditions {
		cond, ok := c.(map[string]any)
		if !ok || asString(cond["status"]) != "True" {
			continue
		}
		switch asString(cond["type"]) {
		case "PodResizePending":
			if strings.EqualFold(asString(cond["reason"]), "Infeasible") {
				return "infeasible"
			}
			return "pending"
		case "PodResizeInProgress":
			if strings.EqualFold(asString(cond["reason"]), "Error") {
				return "infeasible"
			}
			return "inprogress"
		}
	}
	return "done"
}

// runInPlaceRightsizing performs the in-place resize for all of a workload's
// pods in the background and finalizes the agent_task: COMPLETED on success, or
// reset to TODO so the legacy rollout path applies the change with a restart.
func runInPlaceRightsizing(logger *slog.Logger, accountId, taskId, kind, name, namespace string, containers []any) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("in-place rightsizing panic, handing off to rollout", "panic", r, "task_id", taskId)
			setAgentTaskStatus(taskId, "TODO", "")
		}
	}()

	fallback := func(reason string) {
		logger.Info("in-place not applied, handing off to legacy rollout", "reason", reason, "kind", kind, "name", name, "namespace", namespace)
		setAgentTaskStatus(taskId, "TODO", "")
	}

	patchBody, err := resizePatchBody(toResizeContainers(containers))
	if err != nil {
		fallback(err.Error())
		return
	}
	pods, err := listWorkloadPodNames(accountId, kind, name, namespace)
	if err != nil || len(pods) == 0 {
		fallback(fmt.Sprintf("pod enumeration failed: %v", err))
		return
	}

	// Trigger the resize on every pod up front so the kubelets actuate in
	// parallel — total time is ~the poll budget, not budget × replicas (and
	// avoids the agent's PROCESSING -> TIMEOUT cleanup firing on a long run).
	for _, pod := range pods {
		rejected, perr := patchPodResize(accountId, pod, namespace, patchBody)
		if perr != nil {
			fallback(fmt.Sprintf("patch error on %s: %v", pod, perr))
			return
		}
		if rejected {
			fallback(fmt.Sprintf("resize rejected on %s", pod))
			return
		}
	}

	// Poll the pending pods together until all complete, any is Infeasible, or
	// the budget (~60s) is spent.
	pending := make(map[string]struct{}, len(pods))
	for _, pod := range pods {
		pending[pod] = struct{}{}
	}
	for i := 0; i < 12; i++ {
		for pod := range pending {
			switch podResizeStatusOf(accountId, pod, namespace) {
			case "done":
				delete(pending, pod)
			case "infeasible":
				fallback(fmt.Sprintf("resize infeasible on %s", pod))
				return
			}
		}
		if len(pending) == 0 {
			logger.Info("in-place rightsizing applied without restart", "kind", kind, "name", name, "namespace", namespace, "pods", len(pods))
			setAgentTaskStatus(taskId, "COMPLETED", `{"success":true,"mode":"in_place","message":"Applied in-place without restart"}`)
			return
		}
		time.Sleep(5 * time.Second)
	}
	fallback("resize did not complete within budget")
}

// setAgentTaskStatus updates an agent_task's status (and response, if non-empty).
func setAgentTaskStatus(taskId, status, response string) {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		slog.Error("in-place: failed to get db manager", "error", err, "task_id", taskId)
		return
	}
	// Bounded context: this runs in a detached goroutine, so guard the write
	// against a hung DB connection rather than blocking indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if response != "" {
		_, err = dbms.Db.ExecContext(ctx, "UPDATE agent_task SET status = $1, response = $2 WHERE id = $3", status, response, taskId)
	} else {
		_, err = dbms.Db.ExecContext(ctx, "UPDATE agent_task SET status = $1 WHERE id = $2", status, taskId)
	}
	if err != nil {
		slog.Error("in-place: failed to update agent_task status", "error", err, "task_id", taskId, "status", status)
	}
}

// --- small JSON map navigation helpers (avoid an apimachinery dependency) ---

func nestedMap(m map[string]any, keys ...string) (map[string]any, bool) {
	cur := m
	for _, k := range keys {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	return cur, true
}

func nestedString(m map[string]any, keys ...string) string {
	if len(keys) == 0 {
		return ""
	}
	parent, ok := nestedMap(m, keys[:len(keys)-1]...)
	if !ok {
		return ""
	}
	s, _ := parent[keys[len(keys)-1]].(string)
	return s
}

func nestedStringMap(m map[string]any, keys ...string) map[string]string {
	parent, ok := nestedMap(m, keys...)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for k, v := range parent {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func nestedSlice(m map[string]any, keys ...string) ([]any, bool) {
	if len(keys) == 0 {
		return nil, false
	}
	parent, ok := nestedMap(m, keys[:len(keys)-1]...)
	if !ok {
		return nil, false
	}
	s, ok := parent[keys[len(keys)-1]].([]any)
	return s, ok
}
