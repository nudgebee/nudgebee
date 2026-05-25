package playbooks

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// oom_killer_enricher builds a table summarising the OOMed pod:
// Pod / Namespace / Node / Node-allocated-memory / Container /
// Container-memory / Container-started-at / Container-finished-at.
//
// Composes two relay get_resource calls — Pod (for spec.nodeName + container
// state) and Node (for status.allocatable / status.capacity).
type oomKillerAction struct{}

func (a *oomKillerAction) CanAutoExecute(ctx PlaybookActionContext) bool {
	if ctx.GetEvent().AggregationKey != "pod_oom_killer_enricher" {
		return false
	}
	name, ns := subjectPodNamespace(ctx.GetEvent())
	return name != "" && ns != ""
}

func (a *oomKillerAction) AutoExecute(ctx PlaybookActionContext) (PlaybookActionResponse, error) {
	podName, namespace := subjectPodNamespace(ctx.GetEvent())
	return a.Execute(ctx, map[string]any{"pod_name": podName, "namespace": namespace})
}

func (a *oomKillerAction) Execute(ctx PlaybookActionContext, rawParams map[string]any) (PlaybookActionResponse, error) {
	podName, _ := rawParams["pod_name"].(string)
	namespace, _ := rawParams["namespace"].(string)
	if podName == "" || namespace == "" {
		return nil, errors.New("oom_killer_enricher: pod_name + namespace required")
	}

	podData, _, err := getResourceViaRelay(ctx, map[string]any{
		"resource_type":  "pods",
		"group":          "",
		"version":        "v1",
		"namespace":      []string{namespace},
		"all_namespaces": false,
		"name":           []string{podName},
	})
	if err != nil {
		return nil, fmt.Errorf("oom_killer_enricher: get pod: %w", err)
	}
	pod := firstResourceDict(podData)
	if pod == nil {
		return nil, errors.New("oom_killer_enricher: pod not found")
	}

	// Prefer the pre-resolved subject_node from the event (the collector
	// already pulled it off the kubewatch payload). Falls back to the Pod
	// payload we just fetched in case the event came in without it set.
	nodeName := subjectNodeName(ctx.GetEvent())
	if nodeName == "" {
		nodeName = nodeNameFromPodDict(pod)
	}
	headers := []string{"Field", "Value"}
	rows := [][]any{
		{"Pod", podName},
		{"Namespace", namespace},
		{"Node Name", nodeName},
	}

	// Node allocated memory = capacity - allocatable, as a percentage of capacity.
	if nodeName != "" {
		nodeData, _, err := getResourceViaRelay(ctx, map[string]any{
			"resource_type":  "nodes",
			"group":          "",
			"version":        "v1",
			"namespace":      []string{},
			"all_namespaces": false,
			"name":           []string{nodeName},
		})
		if err == nil {
			if node := firstResourceDict(nodeData); node != nil {
				if allocStr, capStr := nodeMemAllocatableCapacity(node); allocStr != "" && capStr != "" {
					alloc, errA := parseK8sMemoryMi(allocStr)
					cap, errC := parseK8sMemoryMi(capStr)
					if errA == nil && errC == nil && cap > 0 {
						pct := float64(cap-alloc) * 100 / float64(cap)
						rows = append(rows, []any{
							"Node allocated memory",
							fmt.Sprintf("%.2f%% out of %dMB allocatable", pct, alloc),
						})
					}
				}
			}
		}
	}

	// Container name + most-recent OOMKilled lastState (see
	// podMostRecentOOMKilledContainer below).
	if container, terminated := podMostRecentOOMKilledContainer(pod); container != nil {
		name, _ := container["name"].(string)
		if name != "" {
			rows = append(rows, []any{"Container name", name})
		}
		req, lim := containerMemoryReqLimit(container)
		var reqLabel, limLabel string
		if req == "" {
			reqLabel = "No request"
		} else {
			reqLabel = req + " request"
		}
		if lim == "" {
			limLabel = "No limit"
		} else {
			limLabel = lim + " limit"
		}
		rows = append(rows, []any{"Container memory", fmt.Sprintf("%s, %s", reqLabel, limLabel)})
		if terminated != nil {
			if s, ok := terminated["started_at"].(string); ok && s != "" {
				rows = append(rows, []any{"Container started at", s})
			} else if s, ok := terminated["startedAt"].(string); ok && s != "" {
				rows = append(rows, []any{"Container started at", s})
			}
			if f, ok := terminated["finished_at"].(string); ok && f != "" {
				rows = append(rows, []any{"Container finished at", f})
			} else if f, ok := terminated["finishedAt"].(string); ok && f != "" {
				rows = append(rows, []any{"Container finished at", f})
			}
		}
	}

	return PlaybookActionResponseTable{
		Rows:    rows,
		Headers: headers,
		AdditionalInfo: map[string]any{
			"title":              "Pod and Node OOMKilled data",
			"action_name":        "oom_killer_enricher",
			"actual_action_name": "oom_killer_enricher",
			"pod_name":           podName,
			"namespace":          namespace,
			"node_name":          nodeName,
		},
		Insight: []PlaybookActionResponseInsight{},
	}, nil
}

// firstResourceDict unwraps the get_resource response. Agent returns
// `findings[0].evidence[0].data` as a JSON-string array of K8s objects when
// called with a name list; we may also receive a single dict for tests.
func firstResourceDict(data any) map[string]any {
	if data == nil {
		return nil
	}
	switch d := data.(type) {
	case map[string]any:
		return d
	case []any:
		if len(d) == 0 {
			return nil
		}
		if m, ok := d[0].(map[string]any); ok {
			return m
		}
	}
	return nil
}

// nodeMemAllocatableCapacity returns (allocatable, capacity) memory strings
// (e.g. "30598688Ki") from a Node payload's status.
func nodeMemAllocatableCapacity(node map[string]any) (string, string) {
	status, ok := node["status"].(map[string]any)
	if !ok {
		return "", ""
	}
	alloc := ""
	cap := ""
	if a, ok := status["allocatable"].(map[string]any); ok {
		if v, ok := a["memory"].(string); ok {
			alloc = v
		}
	}
	if c, ok := status["capacity"].(map[string]any); ok {
		if v, ok := c["memory"].(string); ok {
			cap = v
		}
	}
	return alloc, cap
}

// parseK8sMemoryMi parses K8s quantity strings ("1024Mi", "2Gi", "1048576Ki",
// raw bytes) into MiB.
func parseK8sMemoryMi(s string) (int64, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	s = strings.TrimSpace(s)
	switch {
	case strings.HasSuffix(s, "Ki"):
		s = strings.TrimSuffix(s, "Ki")
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, err
		}
		return v / 1024, nil
	case strings.HasSuffix(s, "Mi"):
		s = strings.TrimSuffix(s, "Mi")
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, err
		}
		return v, nil
	case strings.HasSuffix(s, "Gi"):
		s = strings.TrimSuffix(s, "Gi")
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, err
		}
		return v * 1024, nil
	case strings.HasSuffix(s, "Ti"):
		s = strings.TrimSuffix(s, "Ti")
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, err
		}
		return v * 1024 * 1024, nil
	default:
		// raw bytes
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, err
		}
		return v / (1024 * 1024), nil
	}
}

// podMostRecentOOMKilledContainer walks containerStatuses[] and returns the
// (container, terminated-state) pair where
// lastState.terminated.reason == "OOMKilled".
//
// container is the matching spec.containers[] entry (so caller can read
// resources.requests/limits); terminated is the lastState.terminated dict.
func podMostRecentOOMKilledContainer(pod map[string]any) (map[string]any, map[string]any) {
	status, ok := pod["status"].(map[string]any)
	if !ok {
		return nil, nil
	}
	cs := getArrayField(status, "container_statuses", "containerStatuses")
	if cs == nil {
		return nil, nil
	}
	spec, _ := pod["spec"].(map[string]any)
	specContainers := []any{}
	if spec != nil {
		if v, ok := spec["containers"].([]any); ok {
			specContainers = v
		}
	}
	for _, item := range cs {
		cm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		last := getMapField(cm, "last_state", "lastState")
		if last == nil {
			continue
		}
		term := getMapField(last, "terminated")
		if term == nil {
			continue
		}
		reason, _ := term["reason"].(string)
		if reason != "OOMKilled" {
			continue
		}
		name, _ := cm["name"].(string)
		// Find matching spec container for resource limits/requests.
		for _, sci := range specContainers {
			sc, ok := sci.(map[string]any)
			if !ok {
				continue
			}
			if sName, _ := sc["name"].(string); sName == name {
				return sc, term
			}
		}
		// Spec container not found — still return status info.
		return map[string]any{"name": name}, term
	}
	return nil, nil
}

func containerMemoryReqLimit(container map[string]any) (string, string) {
	res, _ := container["resources"].(map[string]any)
	if res == nil {
		return "", ""
	}
	req, lim := "", ""
	if r, ok := res["requests"].(map[string]any); ok {
		if v, ok := r["memory"].(string); ok {
			req = v
		}
	}
	if l, ok := res["limits"].(map[string]any); ok {
		if v, ok := l["memory"].(string); ok {
			lim = v
		}
	}
	return req, lim
}

func getArrayField(m map[string]any, keys ...string) []any {
	for _, k := range keys {
		if v, ok := m[k].([]any); ok {
			return v
		}
	}
	return nil
}

func getMapField(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		if v, ok := m[k].(map[string]any); ok {
			return v
		}
	}
	return nil
}
