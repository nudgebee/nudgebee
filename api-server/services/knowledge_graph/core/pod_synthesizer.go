package core

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"strings"
	"time"

	"github.com/lib/pq"
)

// PodSynthesizer produces Pod nodes and their edges (Pod→Node RUNS_ON,
// Workload→Pod MANAGES) on demand at KG read time, reading from the
// k8s-collector-owned public.k8s_pods table.
//
// Pods are NOT persisted in knowledge_graph_node / knowledge_graph_edge —
// their lifecycle (seconds–minutes) is incompatible with the nightly
// BuildGraphs cycle. Synthesizing on read keeps the answer fresh
// (seconds, matching k8s_pods freshness) without any write amplification.
//
// Synth identity is deterministic and reversible:
//   - Pod node ID = k8s_pods.cloud_resource_id (a uuid5 the collector
//     already produces from "{ns}/Pod/{name}" + cloud_account_id).
//   - Edge IDs via GenerateEdgeID(src,dst,rel) — same uuid5 shape as
//     persisted edges, so search→get→traverse round-trips work.
type PodSynthesizer struct {
	db     *database.DatabaseManager
	logger *slog.Logger
}

// NewPodSynthesizer constructs a synthesizer bound to a database manager.
func NewPodSynthesizer(db *database.DatabaseManager, logger *slog.Logger) *PodSynthesizer {
	if logger == nil {
		logger = slog.Default()
	}
	return &PodSynthesizer{db: db, logger: logger}
}

// supportedManagesKinds is the set of workload_type values for which a
// Workload entity is emitted by k8s_source — i.e. the only kinds where
// a Workload→Pod MANAGES edge can resolve to a real parent. Pods whose
// owner is a ReplicaSet, Runner, or other unsupported kind still get a
// synth Pod entity and a Pod→Node RUNS_ON edge; they just don't get a
// MANAGES edge (logged at debug).
var supportedManagesKinds = map[string]bool{
	"Deployment":  true,
	"StatefulSet": true,
	"DaemonSet":   true,
	"Job":         true,
	"CronJob":     true,
}

// podRow is the projection of public.k8s_pods columns the synthesizer needs.
type podRow struct {
	CloudResourceID string
	TenantID        string
	CloudAccountID  string
	Name            string
	Namespace       string
	Status          string
	NodeName        string
	WorkloadType    string
	WorkloadName    string
	CreationTime    time.Time
	LastSeen        time.Time
	RestartCount    json.RawMessage
	Labels          map[string]string
	PodIP           string
	HostIP          string
	IsHelmRelease   *bool
}

// podSelect is the column list (and JSON-extracted fields) all pod queries
// share. Column order must match podRow scanning in scanPodRow().
const podSelect = `
	SELECT
		cloud_resource_id::text,
		tenant_id::text,
		cloud_account_id::text,
		name,
		namespace,
		COALESCE(status, '')                                   AS status,
		COALESCE(node_name, '')                                AS node_name,
		COALESCE(workload_type, '')                            AS workload_type,
		COALESCE(workload_name, '')                            AS workload_name,
		COALESCE(creation_time, last_seen)                     AS creation_time,
		last_seen,
		COALESCE(restart_count, '0'::jsonb)                    AS restart_count,
		COALESCE(labels, '{}'::jsonb)                          AS labels,
		COALESCE(meta -> 'config' ->> 'ip', '')                AS pod_ip,
		COALESCE(meta -> 'status_info' ->> 'hostIP', '')       AS host_ip,
		CASE
			WHEN jsonb_typeof(meta -> 'is_helm_release') = 'boolean'
			THEN (meta ->> 'is_helm_release')::boolean
			ELSE NULL
		END                                                    AS is_helm_release
	FROM public.k8s_pods`

// PodByID resolves a synth-Pod node ID (= k8s_pods.cloud_resource_id) to
// an in-memory DbNode. Returns (nil, nil) if no active pod exists for
// the (tenantID, nodeID) pair — the caller treats this as "not found".
func (p *PodSynthesizer) PodByID(tenantID, nodeID string) (*DbNode, error) {
	if p == nil || p.db == nil || tenantID == "" || nodeID == "" {
		return nil, nil
	}
	row, err := p.queryOne(podSelect+`
		WHERE cloud_resource_id = $1::uuid
		  AND tenant_id         = $2::uuid
		  AND is_active = true
		LIMIT 1`, nodeID, tenantID)
	if err != nil || row == nil {
		return nil, err
	}
	cluster := p.clusterFor(tenantID, row.CloudAccountID)
	return row.node(cluster), nil
}

// PodsForNode returns the synth Pod entities running on the given Node
// entity, plus a Pod→Node RUNS_ON edge per pod. The Node entity must be
// a k8s_source-emitted Node; its properties.name is matched against
// k8s_pods.node_name within the same tenant + cloud_account_id.
//
// maxPods caps the result; a value <= 0 disables the cap.
func (p *PodSynthesizer) PodsForNode(node *DbNode, maxPods int) ([]*DbNode, []*DbEdge, error) {
	// Tenant/account must be non-empty before we go to SQL: empty
	// strings would either cast-error on `$2::uuid` (Postgres) or, in
	// the unlikely event the cast accepted them, fail to scope the
	// query. Defense-in-depth — DbNode rows from knowledge_graph_node
	// always carry non-null values, but the guard protects manually
	// constructed callers too.
	if p == nil || node == nil || node.NodeType != NodeTypeNode || node.TenantID == "" || node.CloudAccountID == "" {
		return nil, nil, nil
	}
	nodeName, _ := node.Properties["name"].(string)
	if nodeName == "" {
		return nil, nil, nil
	}
	cluster, _ := node.Properties["cluster"].(string)

	rows, err := p.queryMany(podSelect+`
		WHERE node_name        = $1
		  AND tenant_id        = $2::uuid
		  AND cloud_account_id = $3::uuid
		  AND is_active        = true
		ORDER BY name
		LIMIT $4`,
		nodeName, node.TenantID, node.CloudAccountID, capLimit(maxPods))
	if err != nil {
		return nil, nil, err
	}

	pods := make([]*DbNode, 0, len(rows))
	edges := make([]*DbEdge, 0, len(rows))
	for _, r := range rows {
		pod := r.node(cluster)
		pods = append(pods, pod)
		edges = append(edges, synthEdge(pod.ID, node.ID, RelationshipRunsOn, r.TenantID, r.CloudAccountID, r.LastSeen))
	}
	return pods, edges, nil
}

// PodsForWorkload returns the synth Pod entities managed by the given
// Workload entity, plus a Workload→Pod MANAGES edge per pod.
//
// The match is on (workload_name, workload_type, namespace, tenant,
// account). workload_type is taken from the Workload's properties.kind
// (the value k8s_source stamps); workloads of unsupported kinds (those
// not in supportedManagesKinds) yield no pods because their workload_type
// values won't match real k8s_pods.workload_type entries.
func (p *PodSynthesizer) PodsForWorkload(wl *DbNode, maxPods int) ([]*DbNode, []*DbEdge, error) {
	// Same defense-in-depth rationale as PodsForNode — empty tenant/
	// account would either UUID-cast-error or fail to scope.
	if p == nil || wl == nil || wl.NodeType != NodeTypeWorkload || wl.TenantID == "" || wl.CloudAccountID == "" {
		return nil, nil, nil
	}
	name, _ := wl.Properties["name"].(string)
	kind, _ := wl.Properties["kind"].(string)
	namespace, _ := wl.Properties["namespace"].(string)
	cluster, _ := wl.Properties["cluster"].(string)
	if name == "" || kind == "" || namespace == "" {
		return nil, nil, nil
	}
	if !supportedManagesKinds[kind] {
		// Out-of-scope parent kind (e.g. CRD-backed workload). Don't query.
		return nil, nil, nil
	}

	rows, err := p.queryMany(podSelect+`
		WHERE workload_name    = $1
		  AND workload_type    = $2
		  AND namespace        = $3
		  AND tenant_id        = $4::uuid
		  AND cloud_account_id = $5::uuid
		  AND is_active        = true
		ORDER BY name
		LIMIT $6`,
		name, kind, namespace, wl.TenantID, wl.CloudAccountID, capLimit(maxPods))
	if err != nil {
		return nil, nil, err
	}

	pods := make([]*DbNode, 0, len(rows))
	edges := make([]*DbEdge, 0, len(rows))
	for _, r := range rows {
		pod := r.node(cluster)
		pods = append(pods, pod)
		edges = append(edges, synthEdge(wl.ID, pod.ID, RelationshipManages, r.TenantID, r.CloudAccountID, r.LastSeen))
	}
	return pods, edges, nil
}

// NeighborsForPod resolves the parent Node and (where supported) the
// parent Workload of a synth Pod, returning them as DbNode plus the
// corresponding RUNS_ON / MANAGES edges.
//
// The Pod itself is NOT included in the returned slice — callers append
// the seed separately, matching the shape of GetNodeNeighbors.
//
// Returns (nil, nil, nil) if podID is not a known synth pod.
func (p *PodSynthesizer) NeighborsForPod(tenantID, podID string) (*DbNode, []*DbNode, []*DbEdge, error) {
	if p == nil || tenantID == "" || podID == "" {
		return nil, nil, nil, nil
	}
	row, err := p.queryOne(podSelect+`
		WHERE cloud_resource_id = $1::uuid
		  AND tenant_id         = $2::uuid
		  AND is_active = true
		LIMIT 1`, podID, tenantID)
	if err != nil || row == nil {
		return nil, nil, nil, err
	}
	cluster := p.clusterFor(tenantID, row.CloudAccountID)
	pod := row.node(cluster)

	var neighbors []*DbNode
	var edges []*DbEdge

	if row.NodeName != "" {
		if nodeEnt, lookupErr := p.lookupKgNode(tenantID, row.CloudAccountID, NodeTypeNode, row.NodeName, "", ""); lookupErr == nil && nodeEnt != nil {
			neighbors = append(neighbors, nodeEnt)
			edges = append(edges, synthEdge(pod.ID, nodeEnt.ID, RelationshipRunsOn, row.TenantID, row.CloudAccountID, row.LastSeen))
		}
	}

	if row.WorkloadName != "" && supportedManagesKinds[row.WorkloadType] {
		if wl, lookupErr := p.lookupKgNode(tenantID, row.CloudAccountID, NodeTypeWorkload, row.WorkloadName, row.Namespace, row.WorkloadType); lookupErr == nil && wl != nil {
			neighbors = append(neighbors, wl)
			edges = append(edges, synthEdge(wl.ID, pod.ID, RelationshipManages, row.TenantID, row.CloudAccountID, row.LastSeen))
		}
	}

	return pod, neighbors, edges, nil
}

// SearchPods executes the k8s_pods slice of a SearchNodes call.
// It mirrors SearchNodes' filter semantics (name, name_pattern, namespace,
// cluster, account_ids, labels) and returns SearchNodeResult rows the
// caller can merge with the SQL result.
//
// cluster filtering joins through knowledge_graph_node WHERE
// node_type='Cluster' on the same (tenant, account); a v1 implementation
// trade-off, documented in the spec.
func (p *PodSynthesizer) SearchPods(tenantID string, params SearchNodesParams, limit int) ([]SearchNodeResult, error) {
	if p == nil || p.db == nil || tenantID == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}

	var (
		where = []string{
			"k.tenant_id = $1::uuid",
			"k.is_active = true",
		}
		args   = []interface{}{tenantID}
		argIdx = 2
	)

	if params.Name != "" {
		where = append(where, fmt.Sprintf("k.name = $%d", argIdx))
		args = append(args, params.Name)
		argIdx++
	}
	if params.NamePattern != "" {
		where = append(where, fmt.Sprintf("k.name ILIKE $%d", argIdx))
		args = append(args, params.NamePattern)
		argIdx++
	}
	if params.Namespace != "" {
		where = append(where, fmt.Sprintf("k.namespace = $%d", argIdx))
		args = append(args, params.Namespace)
		argIdx++
	}
	if len(params.AccountIDs) > 0 {
		where = append(where, fmt.Sprintf("k.cloud_account_id = ANY($%d::uuid[])", argIdx))
		args = append(args, pq.Array(params.AccountIDs))
		argIdx++
	}
	for key, value := range params.Labels {
		where = append(where, fmt.Sprintf("k.labels->>$%d = $%d", argIdx, argIdx+1))
		args = append(args, key, value)
		argIdx += 2
	}
	// Cluster filter — join via knowledge_graph_node (Cluster entities)
	// to k8s_pods.cloud_account_id. The join is intentional, not a
	// foreign-key denorm; cluster is the same as the per-account k8s
	// cluster name in nudgebee's model.
	clusterJoin := ""
	if params.Cluster != "" {
		clusterJoin = `
			JOIN public.knowledge_graph_node c
			  ON c.cloud_account_id = k.cloud_account_id
			 AND c.tenant_id        = k.tenant_id
			 AND c.node_type        = 'Cluster'
			 AND c.source           = 'k8s'
			 AND c.is_active        = true
			 AND c.properties->>'name' = $` + fmt.Sprintf("%d", argIdx)
		args = append(args, params.Cluster)
		argIdx++
	}

	query := `
		SELECT
			k.cloud_resource_id::text,
			k.tenant_id::text,
			k.cloud_account_id::text,
			k.name,
			k.namespace,
			COALESCE(k.status, '')                              AS status,
			COALESCE(k.node_name, '')                           AS node_name,
			COALESCE(k.workload_type, '')                       AS workload_type,
			COALESCE(k.workload_name, '')                       AS workload_name,
			COALESCE(k.creation_time, k.last_seen)              AS creation_time,
			k.last_seen,
			COALESCE(k.restart_count, '0'::jsonb)               AS restart_count,
			COALESCE(k.labels, '{}'::jsonb)                     AS labels,
			COALESCE(k.meta -> 'config' ->> 'ip', '')           AS pod_ip,
			COALESCE(k.meta -> 'status_info' ->> 'hostIP', '')  AS host_ip,
			CASE
				WHEN jsonb_typeof(k.meta -> 'is_helm_release') = 'boolean'
				THEN (k.meta ->> 'is_helm_release')::boolean
				ELSE NULL
			END                                                 AS is_helm_release
		FROM public.k8s_pods k` + clusterJoin + `
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY k.name
		LIMIT $` + fmt.Sprintf("%d", argIdx)
	args = append(args, limit)

	rows, err := p.runRows(query, args...)
	if err != nil {
		return nil, err
	}

	results := make([]SearchNodeResult, 0, len(rows))
	for _, r := range rows {
		cluster := p.clusterFor(tenantID, r.CloudAccountID)
		node := r.node(cluster)
		results = append(results, SearchNodeResult{
			ID:             node.ID,
			NodeType:       NodeTypePod,
			Name:           r.Name,
			Namespace:      r.Namespace,
			Cluster:        cluster,
			Source:         "k8s",
			CloudAccountID: r.CloudAccountID,
			Labels:         node.Labels,
			Properties:     node.Properties,
		})
	}
	return results, nil
}

// IsPodSeed checks whether a node ID resolves to a synth Pod for the
// given tenant. Used by GetNodeNeighbors to switch between the standard
// SQL path and the synthesizer's NeighborsForPod path.
func (p *PodSynthesizer) IsPodSeed(tenantID, nodeID string) bool {
	// Today's caller (GetNodeNeighbors) wraps this in `if tenantID != ""`,
	// but a future caller might not — refuse to issue the EXISTS query
	// without both keys to prevent UUID-cast errors and cross-tenant
	// false-positives.
	if p == nil || p.db == nil || tenantID == "" || nodeID == "" {
		return false
	}
	var found bool
	row, err := p.db.QueryRow(`
		SELECT EXISTS (
			SELECT 1 FROM public.k8s_pods
			WHERE cloud_resource_id = $1::uuid
			  AND tenant_id         = $2::uuid
			  AND is_active = true
		)`, nodeID, tenantID)
	if err != nil || row == nil {
		return false
	}
	if scanErr := row.Scan(&found); scanErr != nil {
		return false
	}
	return found
}

// ---------- internals ----------

// capLimit translates the caller's maxPods into a SQL LIMIT. A value
// <= 0 means "no explicit cap"; we still clamp to a hard ceiling so a
// single Workload fan-out (e.g. ingress nginx with 10k replicas) can't
// run away.
const hardPodCap = 500

func capLimit(maxPods int) int {
	if maxPods <= 0 || maxPods > hardPodCap {
		return hardPodCap
	}
	return maxPods
}

func (p *PodSynthesizer) queryOne(query string, args ...interface{}) (*podRow, error) {
	rows, err := p.runRows(query, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

func (p *PodSynthesizer) queryMany(query string, args ...interface{}) ([]*podRow, error) {
	return p.runRows(query, args...)
}

func (p *PodSynthesizer) runRows(query string, args ...interface{}) ([]*podRow, error) {
	rows, err := p.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("pod_synthesizer query: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			p.logger.Warn("pod_synthesizer: rows close failed", "error", closeErr)
		}
	}()

	var out []*podRow
	for rows.Next() {
		r := &podRow{}
		var labelsJSON []byte
		var helmRelease *bool

		if err := rows.Scan(
			&r.CloudResourceID,
			&r.TenantID,
			&r.CloudAccountID,
			&r.Name,
			&r.Namespace,
			&r.Status,
			&r.NodeName,
			&r.WorkloadType,
			&r.WorkloadName,
			&r.CreationTime,
			&r.LastSeen,
			&r.RestartCount,
			&labelsJSON,
			&r.PodIP,
			&r.HostIP,
			&helmRelease,
		); err != nil {
			return nil, fmt.Errorf("pod_synthesizer scan: %w", err)
		}

		r.Labels = unmarshalLabels(labelsJSON)
		r.IsHelmRelease = helmRelease
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pod_synthesizer iter: %w", err)
	}
	return out, nil
}

// unmarshalLabels decodes the k8s_pods.labels JSONB into a string map,
// stringifying non-string values for parity with how NewNode handles
// label extraction in helpers.go.
func unmarshalLabels(raw []byte) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	var asString map[string]string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var asAny map[string]interface{}
	if err := json.Unmarshal(raw, &asAny); err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(asAny))
	for k, v := range asAny {
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}

// node builds a synth DbNode from a row. cluster is resolved by the
// caller (from the parent's properties for fan-out, or via clusterFor
// for direct ID/search lookups). When cluster is empty the field is
// simply omitted from properties / query_attributes.
func (r *podRow) node(cluster string) *DbNode {
	if r == nil {
		return nil
	}
	properties := map[string]interface{}{
		"name":          r.Name,
		"namespace":     r.Namespace,
		"phase":         r.Status,
		"source":        "k8s",
		"creation_time": r.CreationTime.Format(time.RFC3339),
		"last_seen":     r.LastSeen.Format(time.RFC3339),
	}
	if cluster != "" {
		properties["cluster"] = cluster
	}
	if r.NodeName != "" {
		properties["node_name"] = r.NodeName
	}
	if r.WorkloadName != "" {
		properties["workload_name"] = r.WorkloadName
	}
	if r.WorkloadType != "" {
		properties["workload_type"] = r.WorkloadType
	}
	if len(r.RestartCount) > 0 && string(r.RestartCount) != "null" && string(r.RestartCount) != "0" {
		var rc interface{}
		if err := json.Unmarshal(r.RestartCount, &rc); err == nil {
			properties["restart_count"] = rc
		}
	}
	if r.PodIP != "" {
		properties["pod_ip"] = r.PodIP
	}
	if r.HostIP != "" {
		properties["host_ip"] = r.HostIP
	}
	if r.IsHelmRelease != nil {
		properties["is_helm_release"] = *r.IsHelmRelease
	}
	if len(r.Labels) > 0 {
		labelsAsAny := make(map[string]interface{}, len(r.Labels))
		for k, v := range r.Labels {
			labelsAsAny[k] = v
		}
		properties["labels"] = labelsAsAny
	}

	uniqueKey := BuildUniqueKey(CloudProviderK8s, r.CloudAccountID, "", NodeTypePod, r.Namespace, r.Name)

	return &DbNode{
		ID:              r.CloudResourceID,
		NodeType:        NodeTypePod,
		UniqueKey:       uniqueKey,
		Properties:      properties,
		Labels:          r.Labels,
		QueryAttributes: ExtractQueryAttributes(NodeTypePod, properties),
		CloudAccountID:  r.CloudAccountID,
		TenantID:        r.TenantID,
		Level:           "Tenant",
		Source:          "k8s",
		CreatedAt:       r.CreationTime,
		UpdatedAt:       r.LastSeen,
	}
}

// synthEdge builds an in-memory DbEdge for a synth relationship. ID is
// derived deterministically via GenerateEdgeID so callers that
// downstream the same edge twice in one response (e.g. an induced
// subgraph) de-dup naturally on ID.
func synthEdge(srcID, dstID string, rel RelationshipType, tenantID, cloudAccountID string, last time.Time) *DbEdge {
	return &DbEdge{
		ID:                GenerateEdgeID(srcID, dstID, rel),
		SourceNodeID:      srcID,
		DestinationNodeID: dstID,
		RelationshipType:  rel,
		Properties:        map[string]interface{}{"source": "k8s"},
		TenantID:          tenantID,
		CloudAccountID:    cloudAccountID,
		Level:             "Tenant",
		Source:            "k8s",
		IsActive:          true,
		LastSyncVersion:   0,
		CreatedAt:         last,
		UpdatedAt:         last,
	}
}

// clusterFor resolves the K8s cluster name for a (tenant, account) by
// looking up the persisted Cluster entity. Returns "" when none exists
// (very early after onboarding, or when the cron hasn't run yet) — the
// synth Pod will be returned without a cluster property in that case.
func (p *PodSynthesizer) clusterFor(tenantID, cloudAccountID string) string {
	if p == nil || p.db == nil || tenantID == "" {
		return ""
	}
	q := `
		SELECT properties->>'name'
		FROM public.knowledge_graph_node
		WHERE tenant_id = $1::uuid
		  AND node_type = 'Cluster'
		  AND source    = 'k8s'
		  AND is_active = true`
	args := []interface{}{tenantID}
	if cloudAccountID != "" {
		q += ` AND cloud_account_id = $2::uuid`
		args = append(args, cloudAccountID)
	}
	q += ` ORDER BY updated_at DESC LIMIT 1`

	row, err := p.db.QueryRow(q, args...)
	if err != nil || row == nil {
		return ""
	}
	var cluster string
	if scanErr := row.Scan(&cluster); scanErr != nil {
		return ""
	}
	return cluster
}

// lookupKgNode finds a persisted knowledge_graph_node by name + tenant +
// account + node type. For Workload lookups, kind is matched against
// properties.kind and namespace is required.
func (p *PodSynthesizer) lookupKgNode(tenantID, cloudAccountID string, nodeType NodeType, name, namespace, kind string) (*DbNode, error) {
	if p == nil || p.db == nil {
		return nil, nil
	}
	q := `
		SELECT id, created_at, updated_at, properties, labels, query_attributes,
			   cloud_account_id, tenant_id, unique_key, node_type, level, COALESCE(source, '')
		FROM public.knowledge_graph_node
		WHERE tenant_id        = $1::uuid
		  AND cloud_account_id = $2::uuid
		  AND node_type        = $3
		  AND source           = 'k8s'
		  AND is_active        = true
		  AND query_attributes->>'name' = $4`
	args := []interface{}{tenantID, cloudAccountID, string(nodeType), name}
	idx := 5
	if namespace != "" {
		q += fmt.Sprintf(" AND query_attributes->>'namespace' = $%d", idx)
		args = append(args, namespace)
		idx++
	}
	if kind != "" {
		q += fmt.Sprintf(" AND properties->>'kind' = $%d", idx)
		args = append(args, kind)
	}
	q += ` ORDER BY updated_at DESC LIMIT 1`

	rows, err := p.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			p.logger.Warn("pod_synthesizer: lookup rows close failed", "error", closeErr)
		}
	}()

	if !rows.Next() {
		return nil, nil
	}
	node := &DbNode{}
	var propertiesJSON, labelsJSON, queryAttributesJSON []byte
	if err := rows.Scan(
		&node.ID,
		&node.CreatedAt,
		&node.UpdatedAt,
		&propertiesJSON,
		&labelsJSON,
		&queryAttributesJSON,
		&node.CloudAccountID,
		&node.TenantID,
		&node.UniqueKey,
		&node.NodeType,
		&node.Level,
		&node.Source,
	); err != nil {
		return nil, err
	}
	_ = json.Unmarshal(propertiesJSON, &node.Properties)
	_ = json.Unmarshal(labelsJSON, &node.Labels)
	_ = json.Unmarshal(queryAttributesJSON, &node.QueryAttributes)
	return node, nil
}
