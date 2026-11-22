package core

import (
	"net"
	"strings"

	"nudgebee/services/common"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	promlabels "github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

// ExporterWorkloadTokens name the monitoring exporters that scrape OTHER
// resources' metrics. Prometheus stamps every series an exporter emits with the
// exporter's OWN pod/namespace/daemonset labels, so an alert built on those
// series resolves its subject to the exporter rather than to the thing the
// metric describes — a NodeSystemSaturation alert lands on
// `victoria-prometheus-node-exporter-skqnn` instead of the saturated node.
//
// Substring match, not equality: every install helm-prefixes these
// (`victoria-prometheus-node-exporter`, `nudgebee-prometheus-prometheus-node-exporter`).
// That is safe HERE because the caller also requires the alert's rule to be
// node-scoped — a KubePodCrashLooping about the node-exporter pod itself keeps
// its correct subject. Do not reuse this as a standalone "is this a real
// workload" test; resolveSubjectFromLabels deliberately uses an exact match for
// that, because a helm-prefixed exporter CAN be the legitimate subject.
var ExporterWorkloadTokens = []string{"node-exporter", "kube-state-metrics"}

// IsExporterWorkloadName reports whether a subject name is one of the monitoring
// exporters whose labels ride along on every series they scrape.
func IsExporterWorkloadName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" {
		return false
	}
	for _, token := range ExporterWorkloadTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

// nodeScopedExporterJobs are prometheus `job` label values whose series are
// per-NODE by construction: one scrape target per node, every sample describing
// that node. A rule built exclusively on them is about a node even when it never
// names `instance` or `node` explicitly — which most of the kubernetes-mixin
// node rules don't (NodeCPUHighUsage aggregates `without(mode)` and never
// mentions a node dimension).
var nodeScopedExporterJobs = map[string]bool{
	"node-exporter": true,
	"node_exporter": true,
}

// isNodeScopedExporterJob reports whether a `job` label value names a per-node
// exporter. Substring-tolerant because installs helm-prefix the job name the
// same way they prefix the workload.
//
// This is the same signal upstream robusta keys on — it skips the node lookup
// when `job == "kube-state-metrics"` because "instance refers to the IP of
// kube-state-metrics not the node" — applied in the other direction: when the
// job IS a per-node exporter, the series describes a node and the `pod` label
// is only the collector that reported it.
func isNodeScopedExporterJob(job string) bool {
	lower := strings.ToLower(strings.TrimSpace(job))
	if lower == "" {
		return false
	}
	if nodeScopedExporterJobs[lower] {
		return true
	}
	return strings.Contains(lower, "node-exporter") || strings.Contains(lower, "node_exporter")
}

// subNodeDimensionLabels identify something SMALLER than a node. Their presence
// in a rule's expression means the alert has a per-object subject that is not
// the node, so the node rewrite must not fire. `instance` and `node` are
// deliberately absent — they are the node dimensions themselves.
var subNodeDimensionLabels = map[string]bool{
	"namespace": true, "pod": true, "pod_name": true, "container": true,
	"container_name": true, "deployment": true, "statefulset": true,
	"daemonset": true, "replicaset": true, "cronjob": true, "job_name": true,
	"persistentvolumeclaim": true, "horizontalpodautoscaler": true, "hpa": true,
	"service": true, "workload": true, "destination_workload_name": true,
	"source_workload_name": true, "ingress": true, "endpoint": true,
}

// nodeDimensionLabels are the label names that name a node directly.
var nodeDimensionLabels = map[string]bool{
	"node": true, "instance": true,
}

// classifyPromExprNodeScoped reports whether an alert's rule expression
// describes a node. Mirrors classifyPromExprClusterScoped's traversal rules:
// selector matchers, `by (…)` grouping labels (never `without`, which keeps the
// unlisted labels) and `on (…)` matching labels are the only places a label can
// carry an object identity into the result.
//
// Node-scoped means a node signal is present AND no sub-node dimension is. The
// node signal is either an explicit node/instance dimension or a scrape job that
// is per-node (see nodeScopedExporterJobs).
//
// parsed is false when the expression does not parse, so the caller can tell
// "not node-scoped" apart from "unknown".
func classifyPromExprNodeScoped(expr string) (isNode bool, parsed bool) {
	node, err := clusterScopePromQLParser.ParseExpr(expr)
	if err != nil {
		return false, false
	}
	nodeSignal := false
	subNodeSeen := false
	parser.Inspect(node, func(n parser.Node, _ []parser.Node) error {
		switch e := n.(type) {
		case *parser.VectorSelector:
			for _, m := range e.LabelMatchers {
				if m.Name == "__name__" {
					continue
				}
				if subNodeDimensionLabels[m.Name] {
					subNodeSeen = true
				}
				if nodeDimensionLabels[m.Name] {
					nodeSignal = true
				}
				// A per-node scrape job is a node signal on its own. Only an
				// equality matcher counts: `job!="node-exporter"` excludes it.
				if m.Name == "job" && m.Type == promlabels.MatchEqual && nodeScopedExporterJobs[strings.ToLower(m.Value)] {
					nodeSignal = true
				}
			}
		case *parser.AggregateExpr:
			if e.Without {
				return nil
			}
			for _, g := range e.Grouping {
				if subNodeDimensionLabels[g] {
					subNodeSeen = true
				}
				if nodeDimensionLabels[g] {
					nodeSignal = true
				}
			}
		case *parser.BinaryExpr:
			if e.VectorMatching == nil || !e.VectorMatching.On {
				return nil
			}
			for _, l := range e.VectorMatching.MatchingLabels {
				if subNodeDimensionLabels[l] {
					subNodeSeen = true
				}
				if nodeDimensionLabels[l] {
					nodeSignal = true
				}
			}
		}
		return nil
	})
	return nodeSignal && !subNodeSeen, true
}

// isNodeScopedAlert reports whether an alert describes a node, from two
// independent signals.
//
// The `job` label is checked FIRST and on its own: it is present on every node
// alert we see (221/221, 78/78 and 33/33 across the three node alerts on dev),
// it costs no query, and it survives the case the rule text does not — a tenant
// whose prometheus rules never synced has an empty `event_rules.expr`, and
// gating on the expression alone would leave the subject silently wrong for
// exactly those tenants.
//
// The rule expression is the fallback for a node alert whose job label is
// absent or named something local. Keeping both is what makes this robust
// rather than clever: a custom rule with an unrecognised job name still
// resolves, and so does an alert whose rule text we never received.
func isNodeScopedAlert(sc *security.RequestContext, accountId string, in NodeSubjectInput) bool {
	if in.Labels != nil && isNodeScopedExporterJob(in.Labels["job"]) {
		return true
	}
	expr := FetchEventRuleExpr(sc, accountId, in.AlertName)
	if expr == "" {
		return false
	}
	isNode, parsed := classifyPromExprNodeScoped(expr)
	return parsed && isNode
}

// NodeSubjectInput is the subset of an event the node rewrite needs. It is a
// plain struct rather than an *Event so both ingest paths (agent findings and
// relayed webhooks, which carry different event types) can call one resolver.
type NodeSubjectInput struct {
	AlertName        string
	SubjectName      string
	SubjectType      string
	SubjectNamespace string
	Labels           map[string]string
}

// ResolveNodeSubject returns the node a node-scoped alert is really about, or ""
// to leave the event exactly as it is.
//
// It fires only when BOTH hold:
//   - the alert is node-scoped — its `job` label names a per-node exporter, or
//     failing that its rule expression is node-scoped — and
//   - the subject currently resolves to a monitoring exporter.
//
// Requiring both is what keeps a legitimate alert ABOUT an exporter — a
// KubePodCrashLooping on the node-exporter pod, whose rule is pod-scoped —
// pointing at the exporter, which is the correct subject there. Prod carries
// ~280 such events a month.
//
// Resolution order is most to least reliable:
//  1. the `node` label, when the rule kept one;
//  2. the exporter POD name -> k8s_pods.node_name. The label we are rejecting as
//     a subject is the best key to the right answer: the exporter pod runs ON
//     the node the metric describes;
//  3. the `instance` label's host part -> k8s_nodes.internal_ip.
//
// Returning "" on every miss is deliberate: a half-applied rewrite (node type,
// no usable name) would let the node evidence actions run against a name the
// relay cannot find, turning a wrong-subject bug into a failing-enricher bug.
func ResolveNodeSubject(sc *security.RequestContext, accountId string, in NodeSubjectInput) string {
	if accountId == "" || in.AlertName == "" {
		return ""
	}
	// Already a node — nothing to correct (KubeletTooManyPods carries a real
	// `node` label and resolves correctly today).
	if strings.EqualFold(in.SubjectType, "node") {
		return ""
	}
	if !IsExporterWorkloadName(in.SubjectName) {
		return ""
	}
	if !isNodeScopedAlert(sc, accountId, in) {
		return ""
	}

	tenantId := sc.GetSecurityContext().GetTenantId()
	if node := nodeFromLabels(in.Labels); node != "" {
		recordNodeSubjectResult(sc, tenantId, "resolved_label")
		return node
	}
	if node := nodeFromExporterPod(sc, tenantId, accountId, in); node != "" {
		recordNodeSubjectResult(sc, tenantId, "resolved_pod")
		return node
	}
	if node := nodeFromInstanceIP(sc, tenantId, accountId, in.Labels); node != "" {
		recordNodeSubjectResult(sc, tenantId, "resolved_instance_ip")
		return node
	}
	recordNodeSubjectResult(sc, tenantId, "unresolved")
	sc.GetLogger().Info("node_subject: node-scoped alert on an exporter subject, but no node resolved",
		"alert", in.AlertName, "subject_name", in.SubjectName, "account_id", accountId)
	return ""
}

func recordNodeSubjectResult(sc *security.RequestContext, tenantId, result string) {
	common.MetricsSubjectResolution(sc.GetContext(), "prometheus", "node_subject", result, tenantId)
}

// nodeFromLabels reads a node name the rule kept on the firing series. `instance`
// is NOT consulted here — it is a scrape target (`10.0.0.5:9100`), never a node
// name; nodeFromInstanceIP translates it instead.
func nodeFromLabels(labels map[string]string) string {
	if labels == nil {
		return ""
	}
	for _, key := range []string{"node", "instance_node", "kubernetes_node"} {
		if v := strings.TrimSpace(labels[key]); v != "" {
			return v
		}
	}
	return ""
}

// nodeFromExporterPod maps the exporter pod that emitted the series to the node
// it runs on. Only valid when the subject is the POD (the direct-Prometheus
// path); a daemonset subject (the relayed path) names every node at once and
// must not be resolved this way.
func nodeFromExporterPod(sc *security.RequestContext, tenantId, accountId string, in NodeSubjectInput) string {
	if !strings.EqualFold(in.SubjectType, "pod") || in.SubjectName == "" {
		return ""
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ""
	}
	query := `
		SELECT node_name
		FROM k8s_pods
		WHERE tenant_id = $1
		  AND cloud_account_id = $2
		  AND name = $3
		  AND node_name IS NOT NULL AND node_name <> ''`
	args := []any{tenantId, accountId, in.SubjectName}
	if in.SubjectNamespace != "" {
		query += " AND namespace = $4"
		args = append(args, in.SubjectNamespace)
	}
	query += " ORDER BY last_seen DESC NULLS LAST LIMIT 1"

	var nodeName string
	if err := dbms.Db.Get(&nodeName, query, args...); err != nil {
		return ""
	}
	return nodeName
}

// nodeFromInstanceIP translates the `instance` scrape target (`<ip>:<port>`) to
// a node name via the node inventory. Last resort: node IPs are recycled by
// autoscalers, so only a node that is still active counts — a stale match would
// name the wrong machine.
func nodeFromInstanceIP(sc *security.RequestContext, tenantId, accountId string, labels map[string]string) string {
	ip := instanceHost(labels)
	if ip == "" {
		return ""
	}
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return ""
	}
	var nodeName string
	err = dbms.Db.Get(&nodeName, `
		SELECT name
		FROM k8s_nodes
		WHERE tenant_id = $1
		  AND cloud_account_id = $2
		  AND internal_ip = $3
		  AND is_active = true
		ORDER BY updated_at DESC NULLS LAST
		LIMIT 1`, tenantId, accountId, ip)
	if err != nil {
		return ""
	}
	return nodeName
}

// instanceHost returns the host part of the `instance` scrape target
// (`<host>:<port>`), or "" when the label is absent.
func instanceHost(labels map[string]string) string {
	instance := strings.TrimSpace(labels["instance"])
	if instance == "" {
		return ""
	}
	// net.SplitHostPort rather than cutting at the first colon: an IPv6 target is
	// "[fe80::1]:9100", which a naive cut turns into "[fe80". A value with no port
	// at all errors here and is used as-is.
	if host, _, err := net.SplitHostPort(instance); err == nil {
		return host
	}
	return instance
}
