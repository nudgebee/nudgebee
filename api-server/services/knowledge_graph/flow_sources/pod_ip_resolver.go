package flow_sources

import (
	"log/slog"
	"net"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"time"
)

// PodIPResolver resolves a pod IP to the existing Workload (or Pod) node that
// owns it. Companion to K8sServiceIPResolver: that one indexes K8s Service
// ClusterIPs, this one indexes pod IPs. Together they cover the two flavours
// of IP that show up as raw-IP destinations in eBPF/trace flow data.
//
// Built once per K8s account from a single Prometheus kube_pod_info query, so
// per-build cost is one query per account regardless of how many pod IPs ebpf
// observes. Resolution is in-memory after that.
//
// Mirrors K8sServiceIPResolver semantics — same-cluster lookup preferred,
// global-unique fallback when caller cluster is unknown, refuse-to-guess on
// ambiguity across clusters.
type PodIPResolver struct {
	byClusterIP        map[clusterIPKey]*core.DbNode
	byIPAcrossClusters map[string][]*core.DbNode
}

// NewPodIPResolver fetches kube_pod_info from the relay for the given K8s
// account and builds an in-memory pod IP -> existing Workload node index.
//
// We point at *existing* Workload/Pod nodes in existingNodes rather than
// creating new ones — the K8sSource is authoritative for K8s workload
// identity; the resolver just hands back what is already in the graph.
//
// Pods whose owning workload isn't in existingNodes (e.g. a pod from a
// namespace the K8sSource filtered out) are silently skipped: emitting an
// ExternalService is preferable to fabricating a synthetic Workload that
// markInactiveNodes can't tombstone safely.
func NewPodIPResolver(k8sAccountID string, existingNodes []*core.DbNode, logger *slog.Logger) *PodIPResolver {
	r := &PodIPResolver{
		byClusterIP:        make(map[clusterIPKey]*core.DbNode),
		byIPAcrossClusters: make(map[string][]*core.DbNode),
	}

	workloadIdx := indexWorkloadsByOwner(existingNodes)
	if workloadIdx.empty() {
		return r
	}

	// .UTC() is defense in depth: relay/agent format the timestamp as UTC, so
	// the value must be UTC too. The relay-side fix in relay.ExecutePrometheus
	// already converts, but every caller should pass UTC directly to keep the
	// contract local to this function (and survive future relay refactors).
	endTime := time.Now().UTC()
	startTime := endTime.Add(-5 * time.Minute)
	resp, err := relay.ExecutePrometheus(
		k8sAccountID, startTime, endTime,
		map[string]string{"pod_info": `kube_pod_info`},
		true,
	)
	if err != nil {
		if logger != nil {
			logger.Warn("PodIPResolver: kube_pod_info query failed",
				"k8s_account_id", k8sAccountID, "error", err)
		}
		return r
	}

	for _, metric := range extractPodInfoMetrics(resp) {
		podIP, _ := metric["pod_ip"].(string)
		namespace, _ := metric["namespace"].(string)
		cluster, _ := metric["k8s_cluster"].(string)
		createdByKind, _ := metric["created_by_kind"].(string)
		createdByName, _ := metric["created_by_name"].(string)
		podName, _ := metric["pod"].(string)
		if podIP == "" || namespace == "" || createdByName == "" {
			continue
		}
		ownerKind, ownerName := resolveOwner(createdByKind, createdByName)
		node, ok := workloadIdx.lookup(cluster, namespace, ownerKind, ownerName, podName)
		if !ok {
			continue
		}
		if cluster != "" {
			r.byClusterIP[clusterIPKey{cluster, podIP}] = node
		}
		r.byIPAcrossClusters[podIP] = append(r.byIPAcrossClusters[podIP], node)
	}

	if logger != nil {
		logger.Debug("PodIPResolver built",
			"k8s_account_id", k8sAccountID,
			"indexed_pod_ips", len(r.byIPAcrossClusters))
	}
	return r
}

// Resolve returns a Workload/Pod node for the given pod IP, scoped to
// callerCluster. Same semantics as K8sServiceIPResolver.Resolve — see that
// type's docstring for the multi-cluster reasoning.
func (r *PodIPResolver) Resolve(callerCluster, ip string) (*core.DbNode, bool) {
	if r == nil || ip == "" {
		return nil, false
	}
	if callerCluster != "" {
		if n, ok := r.byClusterIP[clusterIPKey{callerCluster, ip}]; ok {
			return n, true
		}
	}
	candidates := r.byIPAcrossClusters[ip]
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return nil, false
}

// ResolveIPToPodWorkload is the canonical entry point for "destination looks
// like an IP — is it a pod IP backing an existing Workload?" Mirrors
// ResolveIPToK8sService: handles port stripping, the special-IP skip list,
// and delegates to PodIPResolver.Resolve.
//
// Returns (matchedNode, reason, ok) where reason is the same constants as the
// ClusterIP resolver — IPResolutionReasonSameCluster / IPResolutionReasonGlobalUnique.
func ResolveIPToPodWorkload(name, callerCluster string, r *PodIPResolver) (*core.DbNode, string, bool) {
	if r == nil || name == "" {
		return nil, "", false
	}
	ip := stripPort(name)
	if ip == "" {
		return nil, "", false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil || isSpecialIP(parsed) {
		return nil, "", false
	}
	node, ok := r.Resolve(callerCluster, ip)
	if !ok {
		return nil, "", false
	}
	reason := IPResolutionReasonSameCluster
	if callerCluster == "" {
		reason = IPResolutionReasonGlobalUnique
	}
	return node, reason, true
}

// Provenance constants for `resolveIPNamedExternalService`. These end up on
// the edge's `resolution_source` property so a reviewer can tell which
// resolver matched a given raw-IP CALLS edge.
const (
	IPResolutionSourceClusterIP = "k8s_cluster_ip_resolver"
	IPResolutionSourcePodIP     = "k8s_pod_ip_resolver"
	IPResolutionSourceNodeIP    = "k8s_node_ip_resolver"
)

// resolveIPNamedExternalService walks the ordered chain of IP resolvers used
// by every flow source's raw-IP ExternalService bypass branch:
//  1. K8sServiceIPResolver — for K8s Service ClusterIPs
//  2. PodIPResolver — for pod IPs (headless services, direct pod-IP traffic)
//  3. K8sNodeIPResolver — for node internal IPs (host-network destinations)
//
// Pod-IP runs before Node-IP because pods are a more specific target than
// the node hosting them. For ambiguous host-network IPs (multiple pods
// sharing a node's primary IP), PodIPResolver correctly refuses to guess
// and K8sNodeIPResolver picks up the slack by attributing to the node itself.
//
// callerCluster scopes all three resolvers to a single cluster when known;
// pass "" to fall through to the resolvers' global-unique fallback. Same-
// cluster scoping prevents wrong-cluster edges in multi-cluster tenants where
// the same IP belongs to different K8s objects in different clusters.
//
// Returns (node, ip, reason, source, ok). `source` distinguishes which
// resolver matched so the resulting edge's provenance is debuggable. ok=false
// means the caller should fall back to creating an orphan ExternalService.
func resolveIPNamedExternalService(
	name, callerCluster string,
	clusterIPResolver *K8sServiceIPResolver,
	podIPResolver *PodIPResolver,
	nodeIPResolver *K8sNodeIPResolver,
) (*core.DbNode, string, string, string, bool) {
	if node, reason, ok := ResolveIPToK8sService(name, callerCluster, clusterIPResolver); ok {
		return node, name, reason, IPResolutionSourceClusterIP, true
	}
	if node, reason, ok := ResolveIPToPodWorkload(name, callerCluster, podIPResolver); ok {
		return node, name, reason, IPResolutionSourcePodIP, true
	}
	if node, reason, ok := ResolveIPToK8sNode(name, callerCluster, nodeIPResolver); ok {
		return node, name, reason, IPResolutionSourceNodeIP, true
	}
	return nil, "", "", "", false
}

// workloadOwnerKey identifies an existing Workload/Pod node by its observable
// K8s identity. (cluster may be empty when the source didn't tag it; the
// index falls back to a cluster-less lookup in that case.)
type workloadOwnerKey struct {
	cluster   string
	namespace string
	kind      string
	name      string
}

type workloadIndex struct {
	withCluster    map[workloadOwnerKey]*core.DbNode
	withoutCluster map[workloadOwnerKey]*core.DbNode
	podsByName     map[workloadOwnerKey]*core.DbNode // (cluster, ns, "Pod", podName)
}

func (idx workloadIndex) empty() bool {
	return len(idx.withCluster) == 0 && len(idx.withoutCluster) == 0 && len(idx.podsByName) == 0
}

func (idx workloadIndex) lookup(cluster, namespace, ownerKind, ownerName, podName string) (*core.DbNode, bool) {
	if podName != "" {
		if n, ok := idx.podsByName[workloadOwnerKey{cluster, namespace, "Pod", podName}]; ok {
			return n, true
		}
		if cluster != "" {
			if n, ok := idx.podsByName[workloadOwnerKey{"", namespace, "Pod", podName}]; ok {
				return n, true
			}
		}
	}
	if ownerKind == "" || ownerName == "" {
		return nil, false
	}
	if n, ok := idx.withCluster[workloadOwnerKey{cluster, namespace, ownerKind, ownerName}]; ok {
		return n, true
	}
	if n, ok := idx.withoutCluster[workloadOwnerKey{"", namespace, ownerKind, ownerName}]; ok {
		return n, true
	}
	return nil, false
}

// indexWorkloadsByOwner builds an index of existing Workload and Pod nodes
// keyed by their K8s identity. Used by NewPodIPResolver to map a
// kube_pod_info row to the node K8sSource already emitted.
func indexWorkloadsByOwner(nodes []*core.DbNode) workloadIndex {
	idx := workloadIndex{
		withCluster:    make(map[workloadOwnerKey]*core.DbNode),
		withoutCluster: make(map[workloadOwnerKey]*core.DbNode),
		podsByName:     make(map[workloadOwnerKey]*core.DbNode),
	}
	for _, n := range nodes {
		if n == nil {
			continue
		}
		if n.NodeType != core.NodeTypeWorkload && n.NodeType != core.NodeTypePod {
			continue
		}
		name := stringProp(n, "name")
		namespace := stringProp(n, "namespace")
		kind := stringProp(n, "kind")
		cluster := stringProp(n, "cluster")
		if name == "" || namespace == "" || kind == "" {
			continue
		}
		key := workloadOwnerKey{cluster, namespace, kind, name}
		idx.withCluster[key] = n
		idx.withoutCluster[workloadOwnerKey{"", namespace, kind, name}] = n
		if n.NodeType == core.NodeTypePod {
			idx.podsByName[workloadOwnerKey{cluster, namespace, "Pod", name}] = n
			idx.podsByName[workloadOwnerKey{"", namespace, "Pod", name}] = n
		}
	}
	return idx
}

// resolveOwner walks one level up from kube_pod_info's created_by_kind /
// created_by_name labels to the Workload that K8sSource emits.
//
// For ReplicaSet-owned pods we infer the parent Deployment via the
// {deployment}-{hash} naming convention rather than issuing a second
// kube_replicaset_owner Prometheus query. The heuristic is the same one
// ip_mapper.go falls back to (helpers.go:ExtractDeploymentFromReplicaSet);
// it is correct for the vast majority of cases and keeps this resolver to a
// single Prometheus call per account.
func resolveOwner(createdByKind, createdByName string) (string, string) {
	if createdByKind == "ReplicaSet" && createdByName != "" {
		return "Deployment", core.ExtractDeploymentFromReplicaSet(createdByName)
	}
	return createdByKind, createdByName
}

// extractPodInfoMetrics flattens the relay's varied Prometheus response shape
// into a list of label maps. Tolerates the three shapes ip_mapper.go already
// handles (top-level keyed list, top-level data list, nested data.pod_info.result).
func extractPodInfoMetrics(resp map[string]interface{}) []map[string]interface{} {
	raw := unwrapPodInfoResult(resp)
	out := make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		pod, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		metric, ok := pod["metric"].(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, metric)
	}
	return out
}

// unwrapPodInfoResult peels the three possible relay response envelopes back
// to the list of result entries. Split out from extractPodInfoMetrics to keep
// each function within complexity budget.
func unwrapPodInfoResult(resp map[string]interface{}) []interface{} {
	if v, ok := resp["pod_info"].([]interface{}); ok {
		return v
	}
	if data, ok := resp["data"].([]interface{}); ok {
		return data
	}
	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		return nil
	}
	pod, ok := data["pod_info"].(map[string]interface{})
	if !ok {
		return nil
	}
	result, _ := pod["result"].([]interface{})
	return result
}
