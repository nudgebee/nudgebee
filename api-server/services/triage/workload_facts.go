package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
)

// workloadFacts holds observed, ground-truth attributes of the workload an alert is about.
// They are gathered OFF the hot path at verdict-mint time so the LLM triage classifier can reason
// over facts — ingress-backing, dependency fan-in, operator-declared labels — instead of guessing
// a workload's blast radius from its name. Every field degrades gracefully: a cloud/non-k8s event,
// an un-inventoried workload, or a missing knowledge graph all leave the corresponding fields at
// their zero value, and `graphKnown` distinguishes "observed to be false/zero" from "not observed".
type workloadFacts struct {
	found          bool
	graphKnown     bool // true when the workload's knowledge-graph node was found (topology facts are real)
	kind           string
	image          string
	appName        string // app.kubernetes.io/name (customer's own logical-app identity)
	partOf         string // app.kubernetes.io/part-of
	component      string // app.kubernetes.io/component
	declaredTier   string // "key=value" of an operator-set tier/criticality label, if any
	customerFacing bool   // ingress/LoadBalancer-backed (observed topology fact; only meaningful when graphKnown)
	fanIn          int    // # of workloads observed calling this one — blast radius (only meaningful when graphKnown)

	// Criticality resolved for the classifier prompt: an operator override (authoritative) if one
	// exists, else the fact-derived level. Empty when the workload has no distinguishing signal.
	criticality          string
	criticalitySource    string // user | fact_signal
	criticalityRationale string
}

// fetchWorkloadFacts resolves the workload an event is about and returns its observed facts.
// Keyed on cloud_resource_id when the event carries it (reliable, ~77% of events) and falling back
// to (namespace, subject_owner). Best-effort throughout — any lookup miss returns what was gathered
// so far rather than an error, because facts only enrich the classifier prompt; they never gate it.
func fetchWorkloadFacts(ctx context.Context, db *sqlx.DB, event *models.Event) workloadFacts {
	var f workloadFacts
	if db == nil || event == nil || event.CloudAccountId == nil {
		return f
	}
	account := *event.CloudAccountId

	// 1. Workload row (kind + labels) from k8s_workloads.
	var row struct {
		Kind   string `db:"kind"`
		Labels string `db:"labels"`
	}
	var err error
	if event.CloudResourceId != nil && *event.CloudResourceId != "" {
		err = db.GetContext(ctx, &row, `
			SELECT kind, COALESCE(labels::text, '{}') AS labels
			FROM k8s_workloads
			WHERE is_active AND cloud_account_id = $1 AND cloud_resource_id::text = $2
			LIMIT 1`, account, *event.CloudResourceId)
	} else if event.SubjectOwner != nil && *event.SubjectOwner != "" && event.SubjectNamespace != nil {
		err = db.GetContext(ctx, &row, `
			SELECT kind, COALESCE(labels::text, '{}') AS labels
			FROM k8s_workloads
			WHERE is_active AND cloud_account_id = $1 AND namespace = $2 AND name = $3
			LIMIT 1`, account, *event.SubjectNamespace, *event.SubjectOwner)
	} else {
		return f
	}
	if err != nil {
		return f // cloud/non-k8s event, or workload not yet inventoried
	}
	f.found = true
	f.kind = row.Kind
	parseWorkloadLabels(row.Labels, &f)

	// 2. Topology facts from the knowledge graph — only when we have the reliable join key.
	if event.CloudResourceId != nil && *event.CloudResourceId != "" {
		fetchGraphFacts(ctx, db, account, *event.CloudResourceId, &f)
	}

	// 3. Derive criticality from the facts, and let an operator override win for the prompt.
	if lvl, _, rat, ok := deriveCriticalityFromFacts(f); ok {
		f.criticality, f.criticalitySource, f.criticalityRationale = lvl, CriticalitySourceFact, rat
	}
	// An operator-declared override (source='user') is authoritative — it replaces the derived level.
	crid := ""
	if event.CloudResourceId != nil {
		crid = *event.CloudResourceId
	}
	if wc, found := resolveWorkloadCriticality(ctx, db, account, crid); found && wc.Source == CriticalitySourceUser {
		f.criticality, f.criticalitySource, f.criticalityRationale = wc.Criticality, wc.Source, wc.Rationale
	}
	return f
}

// parseWorkloadLabels pulls the customer's own identity/tiering labels out of the workload's
// label map. k8s labels are always string→string; a non-conforming map is silently skipped.
func parseWorkloadLabels(labelsJSON string, f *workloadFacts) {
	if labelsJSON == "" {
		return
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(labelsJSON), &m); err != nil {
		return
	}
	f.appName = m["app.kubernetes.io/name"]
	if f.appName == "" {
		f.appName = m["app"]
	}
	f.partOf = m["app.kubernetes.io/part-of"]
	f.component = m["app.kubernetes.io/component"]
	// Respect an operator-declared tier/criticality label if one exists (any key mentioning
	// "criticality" or "tier"): an explicit declaration is the strongest grounding signal.
	for k, v := range m {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "criticality") || strings.Contains(lk, "tier") {
			f.declaredTier = k + "=" + v
			break
		}
	}
}

// fetchGraphFacts fills in the observed topology facts (image, customer-facing, fan-in) from the
// knowledge graph for the workload identified by cloud_resource_id. The graph node join key is
// properties->>'nb_resource_id' (== k8s_workloads.cloud_resource_id, ~99.8% coverage). Customer-facing
// = the workload EXPOSES a K8sService that an Ingress/LoadBalancer routes to; fan-in = the number of
// active CALLS edges pointing at the workload (services observed depending on it).
func fetchGraphFacts(ctx context.Context, db *sqlx.DB, account, cloudResourceID string, f *workloadFacts) {
	var g struct {
		Image          *string `db:"image"`
		CustomerFacing bool    `db:"customer_facing"`
		FanIn          int     `db:"fan_in"`
	}
	err := db.GetContext(ctx, &g, `
		WITH wl AS (
			SELECT id FROM knowledge_graph_node
			WHERE node_type = 'Workload' AND is_active
			  AND cloud_account_id = $1 AND (properties->>'nb_resource_id') = $2
			LIMIT 1
		)
		SELECT
			(SELECT properties->>'primary_image' FROM knowledge_graph_node WHERE id = (SELECT id FROM wl)) AS image,
			EXISTS (
				SELECT 1
				FROM knowledge_graph_edge e
				JOIN knowledge_graph_edge r
				  ON r.destination_node_id = e.destination_node_id
				 AND r.relationship_type IN ('ROUTES_TO_SERVICE', 'ROUTES_TO', 'ROUTES_THROUGH')
				 AND r.is_active AND r.cloud_account_id = $1
				JOIN knowledge_graph_node rs
				  ON rs.id = r.source_node_id AND rs.node_type IN ('Ingress', 'LoadBalancer')
				 AND rs.cloud_account_id = $1
				WHERE e.source_node_id = (SELECT id FROM wl)
				  AND e.relationship_type = 'EXPOSES' AND e.is_active AND e.cloud_account_id = $1
			) AS customer_facing,
			(
				SELECT count(*)
				FROM knowledge_graph_edge e
				JOIN knowledge_graph_node s ON s.id = e.source_node_id AND s.node_type = 'Workload'
				 AND s.cloud_account_id = $1
				WHERE e.destination_node_id = (SELECT id FROM wl)
				  AND e.relationship_type = 'CALLS' AND e.is_active AND e.cloud_account_id = $1
			) AS fan_in
		WHERE EXISTS (SELECT 1 FROM wl)`, account, cloudResourceID)
	if err != nil {
		return // no graph node for this workload — leave graphKnown false
	}
	f.graphKnown = true
	if g.Image != nil {
		f.image = *g.Image
	}
	f.customerFacing = g.CustomerFacing
	f.fanIn = g.FanIn
}

// renderWorkloadFacts appends the observed-fact lines to the classifier's alert context. Emitted
// only for facts we actually have: topology facts (customer_facing/fan_in) are shown only when the
// graph node was found, so their absence is never misread as "observed to be non-critical".
func renderWorkloadFacts(b *strings.Builder, f workloadFacts) {
	if !f.found {
		return
	}
	if f.kind != "" {
		b.WriteString("- workload_kind: " + f.kind + "\n")
	}
	if f.image != "" {
		b.WriteString("- image: " + f.image + "\n")
	}
	if f.appName != "" || f.partOf != "" || f.component != "" {
		b.WriteString("- labels: app=" + f.appName + " part_of=" + f.partOf + " component=" + f.component + "\n")
	}
	if f.declaredTier != "" {
		b.WriteString("- declared_criticality_label: " + f.declaredTier + "\n")
	}
	if f.graphKnown {
		fmt.Fprintf(b, "- customer_facing(observed ingress/LB-backed): %t\n", f.customerFacing)
		fmt.Fprintf(b, "- dependency_fan_in(observed services depending on it): %d\n", f.fanIn)
	}
	if f.criticality != "" {
		fmt.Fprintf(b, "- workload_criticality: %s (source=%s): %s\n", f.criticality, f.criticalitySource, f.criticalityRationale)
	}
}
