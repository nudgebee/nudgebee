package flow_sources

import (
	"net"
	"nudgebee/services/knowledge_graph/core"
	"strings"
)

// K8sServiceIPResolver resolves a K8s Service ClusterIP to an existing Service node.
//
// ClusterIPs are unique within a single cluster, not across clusters. A tenant
// running multiple K8s clusters under the same cloud account routinely has the
// same IP (e.g. 10.0.0.1) bound to different Services in each cluster. The
// resolver therefore primarily indexes on (cluster, cluster_ip) and only falls
// back to a global IP lookup when the caller's cluster is unknown AND the IP is
// globally unique. When the IP is ambiguous globally and the caller cluster is
// unknown, the resolver refuses to guess.
type K8sServiceIPResolver struct {
	byClusterIP        map[clusterIPKey]*core.DbNode
	byIPAcrossClusters map[string][]*core.DbNode
}

type clusterIPKey struct {
	cluster string
	ip      string
}

// NewK8sServiceIPResolver builds the resolver from the existing graph nodes.
// Only K8s Service nodes are indexed. Headless services (cluster_ip = "None"
// or empty) are skipped.
func NewK8sServiceIPResolver(existingNodes []*core.DbNode) *K8sServiceIPResolver {
	byKey := make(map[clusterIPKey]*core.DbNode)
	byIP := make(map[string][]*core.DbNode)

	for _, n := range existingNodes {
		if n == nil || n.NodeType != core.NodeTypeK8sService {
			continue
		}
		ip := stringProp(n, "cluster_ip")
		if ip == "" || ip == "None" || ip == "0.0.0.0" {
			continue
		}
		cluster := stringProp(n, "cluster")
		if cluster != "" {
			byKey[clusterIPKey{cluster, ip}] = n
		}
		byIP[ip] = append(byIP[ip], n)
	}

	return &K8sServiceIPResolver{
		byClusterIP:        byKey,
		byIPAcrossClusters: byIP,
	}
}

// Resolve returns a Service node for the given IP, scoped to callerCluster.
//
// When callerCluster is non-empty, only a same-cluster Service can match. This
// prevents wrong-cluster edges in multi-cluster tenants.
//
// When callerCluster is empty (e.g., the caller is a non-K8s service like an
// EC2 host), the resolver returns the unique node iff exactly one Service has
// this IP across all clusters. Ambiguous lookups return (nil, false) — better
// to emit no edge than a wrong one.
func (r *K8sServiceIPResolver) Resolve(callerCluster, ip string) (*core.DbNode, bool) {
	if r == nil || ip == "" {
		return nil, false
	}
	if callerCluster != "" {
		n, ok := r.byClusterIP[clusterIPKey{callerCluster, ip}]
		return n, ok
	}
	candidates := r.byIPAcrossClusters[ip]
	if len(candidates) == 1 {
		return candidates[0], true
	}
	return nil, false
}

// stringProp reads a string property from a node, tolerating missing or
// non-string values.
func stringProp(n *core.DbNode, key string) string {
	if n == nil || n.Properties == nil {
		return ""
	}
	if v, ok := n.Properties[key].(string); ok {
		return v
	}
	return ""
}

// Resolution reasons returned by ResolveIPToK8sService. Surface in logs and
// edge properties so downstream debugging can tell same-cluster hits from
// global-unique fallback hits.
const (
	IPResolutionReasonSameCluster  = "same_cluster"
	IPResolutionReasonGlobalUnique = "global_unique"
)

// ResolveIPToK8sService is the single canonical entry point for "destination
// looks like an IP — is it a same-cluster K8s ClusterIP?" callers (traces /
// ebpf source-level short-circuits and the K8sServiceIPMatchStrategy enricher
// backstop all go through this).
//
// Owns: port stripping (handles "ip:port" and IPv6 "[::1]:port"), special-IP
// skip list (loopback / link-local / unspecified / cloud metadata), and
// delegation to K8sServiceIPResolver.Resolve with caller-cluster scope.
//
// Returns (matchedNode, reason, ok). reason is IPResolutionReasonSameCluster
// when callerCluster was non-empty and matched, IPResolutionReasonGlobalUnique
// when the IP was globally unique across clusters, "" when ok=false.
func ResolveIPToK8sService(name, callerCluster string, r *K8sServiceIPResolver) (*core.DbNode, string, bool) {
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

// stripPort removes an optional port suffix from an IP literal. Handles:
//   - "10.0.0.1"        → "10.0.0.1"
//   - "10.0.0.1:8000"   → "10.0.0.1"
//   - "[::1]:8000"      → "::1"
//   - "[fe80::1%eth0]"  → "fe80::1%eth0"
//   - "::1"             → "::1"   (bare IPv6, no port)
//
// A bare IPv6 like "fe80::1" contains colons but no port — distinguish by the
// presence of a bracket form or a final ":<digits>" tail.
func stripPort(s string) string {
	if s == "" {
		return ""
	}
	// Bracketed form: [ipv6](:port)?
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return ""
		}
		return s[1:end]
	}
	// IPv4 or "host:port" — exactly one colon means likely host:port.
	if strings.Count(s, ":") == 1 {
		host, _, err := net.SplitHostPort(s)
		if err == nil {
			return host
		}
		// Fall through — could be malformed.
		return strings.SplitN(s, ":", 2)[0]
	}
	// Multiple colons → bare IPv6, no port.
	return s
}

// isSpecialIP returns true for IPs we never try to resolve to a K8s Service:
// loopback, unspecified, link-local (including AWS/GCP metadata 169.254.169.254
// and IPv6 fe80::/10).
func isSpecialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}
	return false
}

// IsSpecialIPName is the string-name counterpart to isSpecialIP, used by flow
// sources to decide whether an unresolvable raw-IP destination should become
// an orphan ExternalService node (no) or be dropped entirely (yes).
//
// A workload "calling 127.0.0.1" or 169.254.169.254 carries no service-topology
// signal — the destination is the workload itself (loopback) or its host's
// cloud metadata endpoint. Creating ExternalService nodes for these only
// pollutes "what does this workload talk to?" queries.
//
// Returns false for empty names, hostnames, and malformed IPs so the caller's
// existing ExternalService fallback path remains unchanged for those.
func IsSpecialIPName(name string) bool {
	if name == "" {
		return false
	}
	ip := stripPort(name)
	if ip == "" {
		return false
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	return isSpecialIP(parsed)
}
