package flow_sources

import (
	"net"
	"nudgebee/services/knowledge_graph/core"
)

// K8sNodeIPResolver resolves a host IP to the existing K8s Node entity that
// owns it. Third sibling to K8sServiceIPResolver (ClusterIP) and
// PodIPResolver (pod IP) — they together cover the three flavours of IP that
// show up as raw-IP destinations in eBPF/trace flow data:
//
//	ClusterIP        — virtual IP front of a Service (kube-proxy / IPVS)
//	pod IP           — secondary IP on the node's ENI, assigned by VPC CNI
//	node internal_ip — the node's primary ENI IP; destination of host-network
//	                   processes (kubelet, aws-node, kube-proxy, amazon-ssm-agent)
//
// PodIPResolver correctly refuses to guess when multiple host-network pods
// share a single pod IP (e.g., aws-node + kube-proxy + node-exporter all
// land at the node's primary IP). This resolver then attributes those
// destinations to the K8s Node itself, which is the semantically correct
// target for host-network traffic.
//
// Built once per BuildFlowRelationships invocation from req.ExistingNodes —
// no Prometheus call needed because K8sSource already emits Node entries
// into the graph with `internal_ip` populated.
type K8sNodeIPResolver struct {
	byClusterIP        map[clusterIPKey]*core.DbNode
	byIPAcrossClusters map[string][]*core.DbNode
}

// NewK8sNodeIPResolver builds the resolver by indexing K8s Node nodes by
// their `internal_ip` property. Nodes without internal_ip are skipped.
// ebpf-emitted "Node" entries (which represent host-network pods, not real
// K8s nodes) are filtered out — only k8s-source Nodes carry the semantic
// the resolver targets.
func NewK8sNodeIPResolver(existingNodes []*core.DbNode) *K8sNodeIPResolver {
	r := &K8sNodeIPResolver{
		byClusterIP:        make(map[clusterIPKey]*core.DbNode),
		byIPAcrossClusters: make(map[string][]*core.DbNode),
	}
	for _, n := range existingNodes {
		if n == nil || n.NodeType != core.NodeTypeNode {
			continue
		}
		if n.Source != "k8s" {
			continue
		}
		ip := stringProp(n, "internal_ip")
		if ip == "" {
			continue
		}
		cluster := stringProp(n, "cluster")
		if cluster != "" {
			r.byClusterIP[clusterIPKey{cluster, ip}] = n
		}
		r.byIPAcrossClusters[ip] = append(r.byIPAcrossClusters[ip], n)
	}
	return r
}

// Resolve returns a K8s Node for the given IP, scoped to callerCluster.
// Same semantics as K8sServiceIPResolver.Resolve and PodIPResolver.Resolve:
// same-cluster preferred, global-unique fallback when caller cluster is
// unknown, refuse to guess on cross-cluster ambiguity.
func (r *K8sNodeIPResolver) Resolve(callerCluster, ip string) (*core.DbNode, bool) {
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

// ResolveIPToK8sNode is the canonical entry point for "destination looks like
// an IP — is it a K8s Node's internal_ip?" Mirrors ResolveIPToK8sService /
// ResolveIPToPodWorkload: handles port stripping, the special-IP skip list,
// and delegates to K8sNodeIPResolver.Resolve.
//
// Returns (matchedNode, reason, ok) where reason is the same constants as
// the other resolvers — IPResolutionReasonSameCluster / IPResolutionReasonGlobalUnique.
func ResolveIPToK8sNode(name, callerCluster string, r *K8sNodeIPResolver) (*core.DbNode, string, bool) {
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
