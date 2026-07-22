package api

import (
	"database/sql"
	"net/http"
	"regexp"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
)

// podHashSuffix matches a Deployment pod's "-<replicaset-hash>-<pod-suffix>" tail and
// rsHashSuffix a bare ReplicaSet "-<hash>" tail. Stripping them recovers the workload
// name a knowledge-graph Workload node is keyed by, for pod/replicaset subjects whose
// owner reference is missing.
var (
	podHashSuffix = regexp.MustCompile(`-[a-f0-9]{6,10}-[a-z0-9]{5}$`)
	rsHashSuffix  = regexp.MustCompile(`-[a-f0-9]{6,10}$`)
)

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// alertRef is a downstream incident correlated to the root by topology + time.
type alertRef struct {
	EventID  string `json:"event_id"`
	Title    string `json:"title"`
	Priority string `json:"priority"`
	Source   string `json:"source"`
	StartsAt string `json:"starts_at"`
}

// impactedNode is a topological dependent of the root, annotated with whether it is
// actively alerting in the incident window. `alerting` dependents are the correlated
// downstream incidents; the rest are potential (topology-only) impact.
type impactedNode struct {
	core.ImpactedService
	Alerting     bool       `json:"alerting"`
	ActiveAlerts []alertRef `json:"active_alerts,omitempty"`
}

func impactKey(ns, name string) string {
	return strings.ToLower(strings.TrimSpace(ns)) + "\x00" + strings.ToLower(strings.TrimSpace(name))
}

// resolveEventSubjectNodeID maps an event's subject to a single knowledge-graph
// Workload/Service node. Pod and ReplicaSet subjects resolve to their owning workload —
// owner reference first, then the hash-stripped subject name. A non-unique or absent
// match returns ok=false so the caller reports "coverage unknown" rather than guessing a
// blast radius. Resolution robustness (namespace-blind collapse, empty service_key) is
// tracked separately in #34569 / #34570 and deliberately not solved here.
func resolveEventSubjectNodeID(kg *core.Service, tenantID, accountID, name, namespace, subjType, owner, ownerKind string) (string, bool) {
	candidates := make([]string, 0, 3)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		for _, c := range candidates {
			if c == s {
				return
			}
		}
		candidates = append(candidates, s)
	}

	// The owner reference is the most reliable workload identity for pod/replicaset subjects.
	switch strings.ToLower(ownerKind) {
	case "deployment", "statefulset", "daemonset", "replicaset":
		add(owner)
	}
	// The subject name, and — for pods/replicasets — its hash-stripped workload form.
	if t := strings.ToLower(subjType); t == "pod" || t == "replicaset" {
		add(podHashSuffix.ReplaceAllString(name, ""))
		add(rsHashSuffix.ReplaceAllString(name, ""))
	}
	add(name)

	// Try node types in priority order (Workload first). A workload and its K8sService /
	// Service share the same name+namespace, so searching all types at once returns >1 and
	// resolves nothing — the workload is the right seed for a blast radius.
	nodeTypeGroups := [][]core.NodeType{
		{core.NodeTypeWorkload},
		{core.NodeTypeService},
		{core.NodeTypeK8sService},
	}
	for _, cand := range candidates {
		for _, nts := range nodeTypeGroups {
			params := core.SearchNodesParams{
				Name:      cand,
				Namespace: namespace,
				NodeTypes: nts,
				Limit:     2,
			}
			if accountID != "" {
				params.AccountIDs = []string{accountID}
			}
			res, err := kg.SearchNodes(tenantID, params)
			if err == nil && res != nil && len(res.Nodes) == 1 {
				return res.Nodes[0].ID, true
			}
		}
	}
	return "", false
}

// annotateImpactedWithActiveAlerts is the topology-driven correlation step: it cross-
// references the root's topological dependents against events that actually fired in the
// incident window, marking each dependent that is alerting. This is what today's
// event-correlation engine cannot produce — it links the root to the *actual* downstream
// alerts it caused, using the knowledge graph (which works) instead of the dead topology-
// walk in the correlation scorer. Dependents with no alert remain as potential impact.
func annotateImpactedWithActiveAlerts(db *sqlx.DB, accountID, rootEventID string, rootTime time.Time, impacted []core.ImpactedService) ([]impactedNode, int) {
	out := make([]impactedNode, len(impacted))
	for i, s := range impacted {
		out[i] = impactedNode{ImpactedService: s}
	}
	if db == nil || accountID == "" || len(impacted) == 0 {
		return out, 0
	}

	// Alerts on a caused-by cascade fire at or shortly after the root; a small lead-in
	// tolerates clock skew and slightly-earlier related alerts.
	const q = `
		SELECT id::text, COALESCE(subject_name,''), COALESCE(subject_namespace,''), COALESCE(subject_owner,''),
		       COALESCE(title,''), COALESCE(NULLIF(computed_priority,''), priority, ''), COALESCE(source,''),
		       starts_at
		FROM events
		WHERE cloud_account_id = $1::uuid
		  AND starts_at >= $2 AND starts_at <= $3
		  AND id <> $4::uuid
		  -- Correlate real alerts only — configuration-change audit rows are not incidents.
		  AND COALESCE(finding_type, '') <> 'configuration_change'
		ORDER BY starts_at DESC
		LIMIT 1000`
	rows, err := db.Query(q, accountID, rootTime.Add(-30*time.Minute), rootTime.Add(2*time.Hour), rootEventID)
	if err != nil {
		return out, 0
	}
	defer func() { _ = rows.Close() }()

	byKey := map[string][]alertRef{}
	for rows.Next() {
		var id, sname, sns, sowner, title, prio, source string
		var startsAt sql.NullTime
		if err := rows.Scan(&id, &sname, &sns, &sowner, &title, &prio, &source, &startsAt); err != nil {
			continue
		}
		ref := alertRef{EventID: id, Title: title, Priority: prio, Source: source}
		if startsAt.Valid {
			ref.StartsAt = startsAt.Time.UTC().Format(time.RFC3339)
		}
		if sname != "" {
			byKey[impactKey(sns, sname)] = append(byKey[impactKey(sns, sname)], ref)
		}
		if sowner != "" && !strings.EqualFold(sowner, sname) {
			byKey[impactKey(sns, sowner)] = append(byKey[impactKey(sns, sowner)], ref)
		}
	}
	if err := rows.Err(); err != nil {
		return out, 0
	}

	correlated := 0
	for i := range out {
		alerts := byKey[impactKey(out[i].Namespace, out[i].Name)]
		if len(alerts) == 0 {
			continue
		}
		if len(alerts) > 3 {
			alerts = alerts[:3]
		}
		out[i].Alerting = true
		out[i].ActiveAlerts = alerts
		correlated++
	}
	return out, correlated
}

// handleEventGetImpact returns the topology-inferred blast radius for an event's subject
// AND the subset of it that is actively alerting — i.e. the downstream incidents this root
// caused, grouped under it. This is the "correlate related alerts into one incident +
// identify the root" the customer asked for, delivered via the knowledge graph (which
// works) rather than the correlation engine (whose cross-service path is dead). It reuses
// the same core.GetImpactedServices primitive the FinOps safety band is built on. See #34627.
func handleEventGetImpact(h *ActionRequest, c *gin.Context, ctx *security.RequestContext) {
	eventID, ok := h.Input["event_id"].(string)
	if !ok || eventID == "" {
		c.JSON(400, common.ErrorActionBadRequest("event_id is required"))
		return
	}

	ev, err := event.GetEvent(ctx, eventID)
	if err != nil {
		ctx.GetLogger().Error("Failed to get event", "error", err, "event_id", eventID)
		c.JSON(400, common.ErrorActionBadRequest("event not found"))
		return
	}

	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		ctx.GetLogger().Error("Failed to get database manager", "error", err)
		c.JSON(400, common.ErrorActionBadRequest("database connection failed"))
		return
	}

	tenantID := ctx.GetSecurityContext().GetTenantId()
	accountID := derefStr(ev.CloudAccountId)
	name := derefStr(ev.SubjectName)
	namespace := derefStr(ev.SubjectNamespace)

	rootTime := time.Now().UTC()
	if ev.StartsAt != nil {
		rootTime = *ev.StartsAt
	} else if ev.CreatedAt != nil {
		rootTime = *ev.CreatedAt
	}

	seed := gin.H{
		"name":      name,
		"namespace": namespace,
		"type":      derefStr(ev.SubjectType),
	}

	kg := core.NewService(ctx, ctx.GetLogger(), dbms)
	nodeID, ok := resolveEventSubjectNodeID(kg, tenantID, accountID, name, namespace,
		derefStr(ev.SubjectType), derefStr(ev.SubjectOwner), derefStr(ev.SubjectOwnerKind))
	if !ok {
		// Subject not resolvable to a single graph node: report unknown coverage rather
		// than an empty blast radius that would read as "nothing impacted".
		c.JSON(http.StatusOK, gin.H{
			"event_id":            eventID,
			"seed":                seed,
			"resolved":            false,
			"impacted":            []impactedNode{},
			"correlated_count":    0,
			"dependent_count":     0,
			"coverage_confidence": string(core.CoverageNone),
		})
		return
	}
	seed["node_id"] = nodeID

	impact, err := kg.GetImpactedServices(tenantID, nodeID, nil, 2)
	if err != nil || impact == nil {
		ctx.GetLogger().Error("Failed to compute blast radius", "error", err, "event_id", eventID, "node_id", nodeID)
		c.JSON(400, common.ErrorActionBadRequest("failed to compute blast radius"))
		return
	}

	// Scope to the subject's namespace: drop cross-namespace false dependents introduced by
	// #34569 name-collision edges, so the blast radius is the subject's same-stack callers.
	deps := impact.Dependents
	if namespace != "" {
		var filtered []core.ImpactedService
		for _, d := range impact.Dependents {
			if strings.EqualFold(d.Namespace, namespace) {
				filtered = append(filtered, d)
			}
		}
		deps = filtered
	}

	// Topology-driven correlation: which dependents are actually alerting in the window.
	impacted, correlatedCount := annotateImpactedWithActiveAlerts(dbms.Db, accountID, eventID, rootTime, deps)

	// Same namespace scope for downstream deps — drop #34569 cross-namespace name-collision
	// edges (e.g. a nudgebee service resolving to every stack's "postgresql"/"redis"/"rabbitmq").
	dependsOn := impact.DownstreamDependencies
	if namespace != "" {
		var fd []core.ImpactedService
		for _, d := range impact.DownstreamDependencies {
			if strings.EqualFold(d.Namespace, namespace) {
				fd = append(fd, d)
			}
		}
		dependsOn = fd
	}

	c.JSON(http.StatusOK, gin.H{
		"event_id":              eventID,
		"seed":                  seed,
		"resolved":              true,
		"impacted":              impacted,        // dependents, each flagged alerting or potential
		"correlated_count":      correlatedCount, // dependents that are actively alerting = the incident
		"depends_on":            dependsOn,       // what the subject depends on = possible cause
		"dependent_count":       impact.DependentCount,
		"production_dependents": impact.ProductionDependents,
		"coverage_confidence":   string(impact.CoverageConfidence),
		"truncated":             impact.Truncated,
	})
}
