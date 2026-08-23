package flow_sources

import (
	"log/slog"
	"net"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/relay"
	"time"
)

// PodIPResolver resolves a pod's IP *or* bare hostname to the existing Workload
// (or Pod) node that owns it. Companion to K8sServiceIPResolver: that one indexes
// K8s Service ClusterIPs, this one indexes pod IPs and pod names. Together they
// cover the flavours of raw destination that show up in eBPF/trace flow data.
//
// Built once per K8s account. The IP index comes from a single Prometheus
// kube_pod_info query; the name index is unioned from that same query *and* the
// k8s-collector's public.k8s_pods table, so name resolution still works for
// accounts that have no Prometheus. Resolution is in-memory after that.
//
// Mirrors K8sServiceIPResolver semantics — same-scope lookup preferred,
// global-unique fallback when the caller scope is unknown, refuse-to-guess on
// ambiguity.
type PodIPResolver struct {
	byClusterIP        map[clusterIPKey]*core.DbNode
	byIPAcrossClusters map[string][]*core.DbNode
	// Pod-name index, keyed by the pod's own namespace because a bare pod
	// hostname (e.g. the StatefulSet ordinal "redis-master-0") only resolves
	// within the caller's namespace under k8s DNS search-path rules.
	byNamespaceName map[nsNameKey]*core.DbNode
	byNameGlobal    map[string][]*core.DbNode
}

// nsNameKey identifies a pod by (namespace, name) for bare-hostname resolution.
type nsNameKey struct {
	namespace string
	name      string
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
		byNamespaceName:    make(map[nsNameKey]*core.DbNode),
		byNameGlobal:       make(map[string][]*core.DbNode),
	}

	workloadIdx := indexWorkloadsByOwner(existingNodes)
	if workloadIdx.empty() {
		return r
	}

	// Source A: kube_pod_info (Prometheus/relay) — builds the IP index and part
	// of the name index. A relay failure is non-fatal: we skip Source A and fall
	// through to Source B (k8s_pods) so accounts without Prometheus still resolve
	// pod names.
	//
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
	} else {
		for _, metric := range extractPodInfoMetrics(resp) {
			podIP, _ := metric["pod_ip"].(string)
			namespace, _ := metric["namespace"].(string)
			cluster, _ := metric["k8s_cluster"].(string)
			createdByKind, _ := metric["created_by_kind"].(string)
			createdByName, _ := metric["created_by_name"].(string)
			podName, _ := metric["pod"].(string)
			if namespace == "" || createdByName == "" {
				continue
			}
			ownerKind, ownerName := resolveOwner(createdByKind, createdByName)
			node, ok := workloadIdx.lookup(cluster, namespace, ownerKind, ownerName, podName)
			if !ok {
				continue
			}
			if podIP != "" {
				if cluster != "" {
					r.byClusterIP[clusterIPKey{cluster, podIP}] = node
				}
				r.byIPAcrossClusters[podIP] = append(r.byIPAcrossClusters[podIP], node)
			}
			if podName != "" {
				if _, seen := r.byNamespaceName[nsNameKey{namespace, podName}]; !seen {
					r.byNamespaceName[nsNameKey{namespace, podName}] = node
				}
			}
		}
	}

	// Source B: k8s_pods (Postgres inventory) — unions the name index, filling
	// gaps and covering accounts with no Prometheus. Non-fatal on error.
	r.addPodNamesFromInventory(k8sAccountID, workloadIdx, logger)

	r.rebuildGlobalNameIndex()

	if logger != nil {
		logger.Debug("PodIPResolver built",
			"k8s_account_id", k8sAccountID,
			"indexed_pod_ips", len(r.byIPAcrossClusters),
			"indexed_pod_names", len(r.byNamespaceName))
	}
	return r
}

// addPodNamesFromInventory unions the pod-name index with public.k8s_pods (the
// k8s-collector inventory). This is the fallback that keeps bare-hostname
// resolution working for accounts with no Prometheus, and fills gaps where the
// kube_pod_info owner heuristic (ReplicaSet->Deployment) failed. Non-fatal: any
// error just leaves the index as Source A built it.
func (r *PodIPResolver) addPodNamesFromInventory(k8sAccountID string, workloadIdx workloadIndex, logger *slog.Logger) {
	if r == nil || k8sAccountID == "" {
		return
	}
	dbManager, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		if logger != nil {
			logger.Warn("PodIPResolver: get database manager failed",
				"k8s_account_id", k8sAccountID, "error", err)
		}
		return
	}
	const query = `
		SELECT namespace, name, COALESCE(workload_type, ''), COALESCE(workload_name, '')
		FROM public.k8s_pods
		WHERE cloud_account_id = $1
		  AND is_active = true
		  AND name <> ''
		  AND workload_name <> ''`
	rows, err := dbManager.Query(query, k8sAccountID)
	if err != nil {
		if logger != nil {
			logger.Warn("PodIPResolver: k8s_pods query failed",
				"k8s_account_id", k8sAccountID, "error", err)
		}
		return
	}
	defer func() { _ = rows.Close() }()

	added := 0
	for rows.Next() {
		var namespace, name, workloadType, workloadName string
		if err := rows.Scan(&namespace, &name, &workloadType, &workloadName); err != nil {
			if logger != nil {
				logger.Warn("PodIPResolver: scan k8s_pods row failed", "error", err)
			}
			continue
		}
		if r.addInventoryPodName(workloadIdx, namespace, name, workloadType, workloadName) {
			added++
		}
	}
	if err := rows.Err(); err != nil {
		if logger != nil {
			logger.Warn("PodIPResolver: iterate k8s_pods rows failed",
				"k8s_account_id", k8sAccountID, "error", err)
		}
	}

	if logger != nil {
		logger.Debug("PodIPResolver inventory names added",
			"k8s_account_id", k8sAccountID, "added", added)
	}
}

// addInventoryPodName maps one k8s_pods row (namespace, pod name, owning
// workload) to the existing Workload node via workloadIdx and adds it to the
// name index when the (namespace, name) key isn't already populated (Source A
// wins). Returns true when it added an entry. Pure (no DB), so the row->node
// mapping is unit-testable without the metastore singleton.
func (r *PodIPResolver) addInventoryPodName(workloadIdx workloadIndex, namespace, name, workloadType, workloadName string) bool {
	if r == nil || namespace == "" || name == "" || workloadName == "" {
		return false
	}
	if _, seen := r.byNamespaceName[nsNameKey{namespace, name}]; seen {
		return false
	}
	node, ok := workloadIdx.lookup("", namespace, workloadType, workloadName, name)
	if !ok {
		return false
	}
	r.byNamespaceName[nsNameKey{namespace, name}] = node
	return true
}

// rebuildGlobalNameIndex derives byNameGlobal from the deduped byNamespaceName
// map — one entry per (namespace, name) — so a pod reported by both sources is
// counted once, keeping the global-unique fallback (used when the caller
// namespace is unknown) correct. Call after both sources have populated
// byNamespaceName.
func (r *PodIPResolver) rebuildGlobalNameIndex() {
	if r == nil {
		return
	}
	r.byNameGlobal = make(map[string][]*core.DbNode, len(r.byNamespaceName))
	for k, node := range r.byNamespaceName {
		r.byNameGlobal[k.name] = append(r.byNameGlobal[k.name], node)
	}
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

// ResolvePodName resolves a bare pod hostname (e.g. the StatefulSet ordinal
// "redis-master-0") to the existing Workload node that owns it. A bare pod
// hostname only resolves within the caller's own namespace under k8s DNS
// search-path rules, so callerNamespace is the authoritative disambiguator:
//   - namespace known  -> exact (namespace, name) lookup; a miss means the name
//     is not a same-namespace pod, so return false (treat as external).
//   - namespace unknown -> resolve only when the name is globally unique across
//     the account's pods; refuse to guess otherwise.
//
// Mirrors Resolve's same-scope-preferred / global-unique / refuse-to-guess contract.
func (r *PodIPResolver) ResolvePodName(callerNamespace, name string) (*core.DbNode, bool) {
	if r == nil || name == "" {
		return nil, false
	}
	if callerNamespace != "" {
		if n, ok := r.byNamespaceName[nsNameKey{callerNamespace, name}]; ok {
			return n, true
		}
		return nil, false
	}
	if candidates := r.byNameGlobal[name]; len(candidates) == 1 {
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
