package api

import (
	"database/sql"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/event"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"nudgebee/services/triage"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
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

// scopeAndNormalize prepares a knowledge-graph dependency list for correlation:
//
//  1. Namespace scoping — drop the cross-namespace dependents #34569's name-only graph
//     matching invents (a "postgresql" in one stack picking up every other stack's
//     callers). An empty seed namespace scopes nothing away.
//  2. ReplicaSet normalization — the graph holds the SAME workload under two names,
//     the bare one and the ReplicaSet-suffixed one (observed together in one response:
//     "load-generator" and "load-generator-86b88dd659", "product-reviews" and
//     "product-reviews-76cf66f66b"). Events, by contrast, always carry the bare owning
//     workload in subject_owner, so the suffixed copy can never match an alert: it is
//     reported as a second, permanently non-alerting dependent, and its topology key
//     never lines up with any alert identity, so a genuine downstream alert cannot
//     reach the impact tier through it.
//  3. Dedup — after normalization the two copies collapse to one, keeping the shallowest
//     hop count (the closest path is the one worth reporting).
//
// Normalization routes through triage.WorkloadName, the same rule the event side uses,
// so there is one definition of "which workload is this" on both sides of the match.
func scopeAndNormalize(services []core.ImpactedService, namespace string) []core.ImpactedService {
	var out []core.ImpactedService
	seen := map[string]int{} // normalized key -> index in out
	for _, s := range services {
		if namespace != "" && !strings.EqualFold(s.Namespace, namespace) {
			continue
		}
		s.Name = triage.WorkloadName(s.Name)
		if s.Name == "" {
			continue
		}
		k := impactKey(s.Namespace, s.Name)
		if i, ok := seen[k]; ok {
			if s.HopsAway < out[i].HopsAway {
				out[i] = s
			}
			continue
		}
		seen[k] = len(out)
		out = append(out, s)
	}
	return out
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
	//
	// Each group holds a single type for that reason, and the same applies to cloud
	// resources: an AWS resource routinely shares its name with a security group
	// (`nb-demo-db` is both a Database and a SecurityGroup), so a combined group
	// would return 2 and resolve nothing.
	groups := seedNodeTypeGroups()
	for _, cand := range candidates {
		for _, group := range groups {
			params := core.SearchNodesParams{
				Name: cand,
				// Kubernetes names are unique only within a namespace, so the
				// namespace must be part of the match. Cloud events carry the
				// provider's service code there instead (AmazonRDS, AWSELB) while
				// cloud nodes carry no namespace at all, so matching on it could
				// only ever fail — those groups are searched namespace-blind, and
				// the node-type filter keeps that from reaching K8s nodes.
				Namespace: group.namespace(namespace),
				NodeTypes: []core.NodeType{group.nodeType},
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

// seedNodeTypeGroup is one attempt at resolving a subject: a node type plus
// whether the event's namespace applies to it.
type seedNodeTypeGroup struct {
	nodeType   core.NodeType
	namespaced bool
}

func (g seedNodeTypeGroup) namespace(eventNamespace string) string {
	if g.namespaced {
		return eventNamespace
	}
	return ""
}

// k8sSeedPriority is the order Kubernetes types are tried in, and the set that is
// matched with the event's namespace. It is explicit because the ordering encodes
// a real preference — a workload is a better blast-radius seed than the service
// in front of it — which no generic rule would reproduce.
var k8sSeedPriority = []core.NodeType{
	core.NodeTypeWorkload,
	core.NodeTypeService,
	core.NodeTypeK8sService,
}

// seedNodeTypeGroups lists every resolvable seed type: the Kubernetes ones in
// preference order, then every other type the knowledge graph defines a
// blast-radius traversal for. Deriving the remainder from
// core.ImpactSeedNodeTypes keeps this from becoming a second list that drifts —
// a node type gains a traversal and becomes resolvable in the same edit.
// Built once at init: both inputs are static, and this sits on the event
// resolution path. The slice is shared rather than copied per call — it is
// unexported, read-only at every use, and the elements are value types.
var seedGroups = func() []seedNodeTypeGroup {
	seen := make(map[core.NodeType]bool, len(k8sSeedPriority))
	groups := make([]seedNodeTypeGroup, 0, len(k8sSeedPriority))
	for _, nodeType := range k8sSeedPriority {
		seen[nodeType] = true
		groups = append(groups, seedNodeTypeGroup{nodeType: nodeType, namespaced: true})
	}
	for _, nodeType := range core.ImpactSeedNodeTypes() {
		if seen[nodeType] {
			continue
		}
		groups = append(groups, seedNodeTypeGroup{nodeType: nodeType})
	}
	return groups
}()

func seedNodeTypeGroups() []seedNodeTypeGroup { return seedGroups }

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
			// Dependent names arrive workload-normalized (scopeAndNormalize), so an
			// ownerless event whose subject is itself a ReplicaSet must be indexed under
			// its workload too, or it can never be matched back.
			if wn := triage.WorkloadName(sname); wn != strings.ToLower(strings.TrimSpace(sname)) {
				byKey[impactKey(sns, wn)] = append(byKey[impactKey(sns, wn)], ref)
			}
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
	seedIdentity := triage.AlertIdentity{
		ID:               eventID,
		SubjectType:      derefStr(ev.SubjectType),
		SubjectName:      name,
		SubjectNamespace: namespace,
		SubjectOwner:     derefStr(ev.SubjectOwner),
		AggregationKey:   derefStr(ev.AggregationKey),
	}
	dependsOnMap := map[string][]string{}

	kg := core.NewService(ctx, ctx.GetLogger(), dbms)
	nodeID, ok := resolveEventSubjectNodeID(kg, tenantID, accountID, name, namespace,
		derefStr(ev.SubjectType), derefStr(ev.SubjectOwner), derefStr(ev.SubjectOwnerKind))
	if !ok {
		// Subject not resolvable to a single graph node: report unknown coverage rather
		// than an empty blast radius that would read as "nothing impacted". The
		// same-subject and seed-deploy tiers still resolve from the window alone.
		c.JSON(http.StatusOK, gin.H{
			"event_id":            eventID,
			"seed":                seed,
			"resolved":            false,
			"impacted":            []impactedNode{},
			"correlated_count":    0,
			"dependent_count":     0,
			"coverage_confidence": string(core.CoverageNone),
			"assembly":            buildIncidentAssembly(ctx.GetLogger(), dbms.Db, accountID, eventID, rootTime, seedIdentity, dependsOnMap),
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

	// Scope to the subject's namespace and collapse the graph's duplicate ReplicaSet-named
	// copies onto the workload identity the event side uses — see scopeAndNormalize.
	deps := scopeAndNormalize(impact.Dependents, namespace)
	dependsOn := scopeAndNormalize(impact.DownstreamDependencies, namespace)

	// Topology for the tiering: the subject depends on its downstream dependencies
	// (cause side); its dependents depend on it (impact side). Keys go through
	// SubjectKey so they match the candidate identities (same lowercasing/normalization).
	// SubjectName, not SubjectOwner: the owner branch is taken verbatim, so a name that
	// still carried a ReplicaSet hash would key the topology under an identity no alert
	// can ever have. scopeAndNormalize has already stripped it; this keeps the two in
	// agreement even if a caller passes an unnormalized list.
	seedKey := triage.SubjectKey(seedIdentity)
	topoKey := func(s core.ImpactedService) string {
		return triage.SubjectKey(triage.AlertIdentity{SubjectNamespace: s.Namespace, SubjectName: s.Name})
	}
	for _, u := range dependsOn {
		dependsOnMap[seedKey] = append(dependsOnMap[seedKey], topoKey(u))
	}
	for _, d := range deps {
		dk := topoKey(d)
		dependsOnMap[dk] = append(dependsOnMap[dk], seedKey)
	}

	// Topology-driven correlation: which dependents are actually alerting in the window.
	impacted, correlatedCount := annotateImpactedWithActiveAlerts(dbms.Db, accountID, eventID, rootTime, deps)
	assembly := buildIncidentAssembly(ctx.GetLogger(), dbms.Db, accountID, eventID, rootTime, seedIdentity, dependsOnMap)

	c.JSON(http.StatusOK, gin.H{
		"event_id":              eventID,
		"seed":                  seed,
		"resolved":              true,
		"impacted":              impacted,        // dependents, each flagged alerting or potential
		"correlated_count":      correlatedCount, // dependents that are actively alerting = the incident
		"depends_on":            dependsOn,       // what the subject depends on = possible cause
		"dependent_count":       impact.DependentCount,
		"production_dependents": impact.ProductionDependents,
		// Dependents that were traversed but are not application-level types.
		// Reported separately because "not a service that breaks" assumes a
		// Kubernetes-shaped split between infrastructure and workloads: on a VM
		// deployment the ComputeInstance calling the database is the application,
		// and omitting it makes a real blast radius read as "nothing impacted".
		// Deliberately not folded into dependent_count — that feeds the FinOps
		// safety band.
		// Normalised but deliberately NOT namespace-scoped: infrastructure nodes
		// are cluster- or account-scoped and carry no namespace (a cloud resource
		// has none at all, a k8s Node is cluster-wide), so scoping them by the
		// event's namespace discards every one of them.
		"infrastructure_impacted": scopeAndNormalize(impact.InfrastructureDependents, ""),
		"infrastructure_count":    impact.InfrastructureCount,
		"coverage_confidence":     string(impact.CoverageConfidence),
		"truncated":               impact.Truncated,
		"assembly":                assembly, // four-tier incident story (#34658)
	})
}

// tierItem is one logical alert placed in an incident tier — collapsed across its
// in-window firings and cross-source copies — with its offset from the root and,
// for the rarity-gated tiers, how unusual the alert is for its subject.
type tierItem struct {
	EventID         string        `json:"event_id"`
	Title           string        `json:"title,omitempty"`
	Subject         string        `json:"subject,omitempty"`
	AggregationKey  string        `json:"aggregation_key,omitempty"`
	Priority        string        `json:"priority,omitempty"`
	Sources         []string      `json:"sources,omitempty"`          // unioned across cross-source copies
	OccurrenceCount int           `json:"occurrence_count,omitempty"` // firings collapsed into this item, in-window
	Relation        string        `json:"relation,omitempty"`         // how it's wired to the seed (triage.Relation*)
	StartsAt        string        `json:"starts_at,omitempty"`
	DtSeconds       int           `json:"dt_seconds"`
	Evidence        *tierEvidence `json:"evidence,omitempty"`
}

// tierEvidence is the rarity fact behind a chronic fold or a kept impact: how many
// firings the subject's own history predicted for the window vs how many occurred.
type tierEvidence struct {
	ExpectedInWindow float64 `json:"expected_in_window"`
	ObservedInWindow float64 `json:"observed_in_window"`
}

// windowRow is a fetched window event plus the identity the tiering keys on.
type windowRow struct {
	id       triage.AlertIdentity
	title    string
	priority string
	source   string
	startsAt time.Time
	fp       string
}

// buildIncidentAssembly computes the deterministic four-tier incident story for the
// seed (#34658). It fetches the incident window once, computes each candidate's
// trailing firing rate, runs the pure triage.AssembleTiers (the same function the
// replay harness scores), then collapses each tier so a recurring alert appears once
// with an occurrence count rather than as N near-identical rows. It is additive: on
// any DB error it returns an empty-but-valid assembly so the rest of the panel
// response is unaffected. Runs for unresolved seeds too (empty topology →
// same_incident + seed config-changes only).
func buildIncidentAssembly(log *slog.Logger, db *sqlx.DB, accountID, rootEventID string, rootTime time.Time, seed triage.AlertIdentity, dependsOn map[string][]string) gin.H {
	windowMeta := gin.H{"lead_in_s": 7200, "core_s": 7200, "impact_s": 7200}
	empty := gin.H{
		"root_identity":    triage.SubjectKey(seed),
		"same_incident":    []tierItem{},
		"cause":            gin.H{"config_changes": []tierItem{}, "upstream": []tierItem{}},
		"impact":           []tierItem{},
		"chronic":          []tierItem{},
		"window":           windowMeta,
		"truncated":        false,
		"seed_occurrences": 1,
	}
	if db == nil || accountID == "" {
		return empty
	}

	wr, truncated := fetchWindowRows(log, db, accountID, rootEventID, rootTime)
	if len(wr) == 0 {
		return empty
	}

	rates := computeCandidateRates(log, db, accountID, rootTime, wr)

	cands := make([]triage.AlertIdentity, len(wr))
	for i := range wr {
		cands[i] = wr[i].id
	}
	tiers := triage.AssembleTiers(seed, cands, dependsOn, rates)

	// Collapse: one item per (tier, SubjectKey, aggregation_key). Rows arrive newest-
	// first (ORDER BY starts_at DESC), so the first row of each group is its newest and
	// stays the representative; occurrence_count is the in-window firing count and
	// sources unions cross-source copies (Prometheus + PagerDuty of one alert). Without
	// this, a recurring alert on a noisy subject would list many near-identical rows.
	type ckey struct{ tier, subj, agg string }
	type citem struct {
		tier  string
		rep   windowRow
		count int
		src   map[string]bool
	}
	seedOccurrences := 1 // the seed row itself, which is never part of wr

	seen := map[ckey]int{}
	var items []citem
	for _, r := range wr {
		tier, ok := tiers[r.id.ID]
		if !ok {
			continue
		}
		if tier == triage.TierCore && isSeedRefiring(seed, r.id) {
			seedOccurrences++
			continue
		}
		k := ckey{tier, triage.SubjectKey(r.id), r.id.AggregationKey}
		i, ok2 := seen[k]
		if !ok2 {
			i = len(items)
			seen[k] = i
			items = append(items, citem{tier: tier, rep: r, src: map[string]bool{}})
		}
		items[i].count++
		if r.source != "" {
			items[i].src[r.source] = true
		}
	}

	sameIncident, configChanges, upstream, impact, chronic := []tierItem{}, []tierItem{}, []tierItem{}, []tierItem{}, []tierItem{}
	for _, ci := range items {
		item := toTierItem(ci.rep, rates)
		item.OccurrenceCount = ci.count
		item.Relation = triage.RelationTo(seed, ci.rep.id, dependsOn)
		for s := range ci.src {
			item.Sources = append(item.Sources, s)
		}
		sort.Strings(item.Sources)
		switch ci.tier {
		case triage.TierCore:
			sameIncident = append(sameIncident, item)
		case triage.TierCause:
			if ci.rep.id.IsConfigChange {
				configChanges = append(configChanges, item)
			} else {
				upstream = append(upstream, item)
			}
		case triage.TierImpact:
			impact = append(impact, item)
		case triage.TierChronic:
			chronic = append(chronic, item)
		}
	}

	return gin.H{
		"root_identity":    triage.SubjectKey(seed),
		"same_incident":    sameIncident,
		"cause":            gin.H{"config_changes": configChanges, "upstream": upstream},
		"impact":           impact,
		"chronic":          chronic,
		"window":           windowMeta,
		"truncated":        truncated,
		"seed_occurrences": seedOccurrences,
	}
}

// isSeedRefiring reports whether a window row is a later firing of the seed's OWN alert
// rather than a different alert on the same subject.
//
// The seed's row is excluded from the window fetch by id, but a re-firing an hour later
// is a separate row with a separate id. Left in, it renders under "more alerts on
// <service>" carrying the exact title of the alert the viewer already has open — the
// panel claims two more alerts on checkout, and one of them IS checkout's alert.
//
// Identity is (subject, aggregation_key): the aggregation key is the alert rule, so the
// same rule on the same workload is the same alert. A row is NOT matched when the seed
// has no aggregation key — that would swallow every core row on the seed's subject.
func isSeedRefiring(seed, cand triage.AlertIdentity) bool {
	return seed.AggregationKey != "" && cand.AggregationKey == seed.AggregationKey &&
		triage.SubjectKey(cand) == triage.SubjectKey(seed)
}

// windowFetchLimit bounds the window fetch; hitting it sets assembly.truncated.
const windowFetchLimit = 1000

// fetchWindowRows loads the incident window [root-2h, root+2h] once. Unlike the
// legacy annotate query it keeps configuration-change rows (the cause tier consumes
// them) and returns each row's fingerprint (for the rarity count) and its offset
// from the root. Errors are logged and yield an empty slice so assembly degrades to
// empty, not a failed response. The bool reports whether the LIMIT was hit.
func fetchWindowRows(log *slog.Logger, db *sqlx.DB, accountID, rootEventID string, rootTime time.Time) ([]windowRow, bool) {
	const winQ = `
		SELECT id::text, COALESCE(fingerprint,''), COALESCE(subject_name,''), COALESCE(subject_namespace,''),
		       COALESCE(subject_owner,''), COALESCE(subject_type,''), COALESCE(aggregation_key,''),
		       COALESCE(title,''), COALESCE(NULLIF(computed_priority,''), priority, ''), COALESCE(source,''),
		       starts_at, COALESCE(finding_type,'')
		FROM events
		WHERE cloud_account_id = $1::uuid
		  AND starts_at >= $2 AND starts_at <= $3
		  AND id <> $4::uuid
		ORDER BY starts_at DESC
		LIMIT 1000`
	rows, err := db.Query(winQ, accountID, rootTime.Add(-2*time.Hour), rootTime.Add(2*time.Hour), rootEventID)
	if err != nil {
		if log != nil {
			log.Warn("incident assembly: window query failed", "error", err, "account", accountID)
		}
		return nil, false
	}
	defer func() { _ = rows.Close() }()

	var wr []windowRow
	for rows.Next() {
		var id, fp, sname, sns, sowner, stype, aggk, title, prio, source, ftype string
		var startsAt sql.NullTime
		if err := rows.Scan(&id, &fp, &sname, &sns, &sowner, &stype, &aggk, &title, &prio, &source, &startsAt, &ftype); err != nil {
			// A schema/driver mismatch would otherwise fail every row silently and
			// return an empty assembly that looks like "nothing related".
			if log != nil {
				log.Warn("incident assembly: window row scan failed", "error", err, "account", accountID)
			}
			continue
		}
		var sa time.Time
		dt := 0
		if startsAt.Valid {
			sa = startsAt.Time
			dt = int(sa.Sub(rootTime).Seconds())
		}
		wr = append(wr, windowRow{
			id: triage.AlertIdentity{
				ID: id, SubjectType: stype, SubjectName: sname, SubjectNamespace: sns,
				SubjectOwner: sowner, AggregationKey: aggk, TsOffsetS: dt,
				IsConfigChange: ftype == "configuration_change",
				FindingType:    ftype,
			},
			title: title, priority: prio, source: source, startsAt: sa, fp: fp,
		})
	}
	if err := rows.Err(); err != nil {
		if log != nil {
			log.Warn("incident assembly: window scan failed", "error", err, "account", accountID)
		}
		return nil, false
	}
	return wr, len(wr) >= windowFetchLimit
}

// computeCandidateRates returns each candidate's trailing firing rate keyed by
// triage.RateKey. It counts firings per fingerprint over the 7 days BEFORE the
// incident (one batched query, covered by events_fingerprint_account_starts_idx — no
// new index) and sums them into (SubjectKey|aggregation_key) buckets so ReplicaSet-name
// rotation is absorbed (rotation undercounts, the safe direction — less suppression).
// Observed is the count actually in the current window.
func computeCandidateRates(log *slog.Logger, db *sqlx.DB, accountID string, rootTime time.Time, wr []windowRow) map[string]triage.Rate {
	observed := map[string]float64{}
	fpBucket := map[string]string{}
	fps := []string{}
	for _, r := range wr {
		observed[triage.RateKey(r.id)]++
		if r.fp == "" {
			continue
		}
		if _, ok := fpBucket[r.fp]; !ok {
			fpBucket[r.fp] = triage.RateKey(r.id)
			fps = append(fps, r.fp)
		}
	}

	sum7d := queryFingerprintCounts(log, db, accountID, rootTime, fps, fpBucket)

	const windowDays = 4.0 / 24.0 // the [root-2h, root+2h] fetch window
	rates := make(map[string]triage.Rate, len(observed))
	for bucket, o := range observed {
		rates[bucket] = triage.Rate{Expected: (float64(sum7d[bucket]) / 7.0) * windowDays, Observed: o}
	}
	return rates
}

// queryFingerprintCounts sums each fingerprint's firing count over the 7 days BEFORE
// the incident into the caller's (SubjectKey|aggregation_key) buckets. One batched
// query covered by events_fingerprint_account_starts_idx.
//
// The window is anchored to rootTime, NOT to now(): "how unusual is this alert" has to
// mean "as of the incident", otherwise opening a month-old incident would measure it
// against the last 7 days of today. A service that has since gone quiet would look
// rare in hindsight and its noise would be promoted to real impact. Anchoring also
// keeps the incident's own burst out of its baseline, which sharpens burst detection.
// The $3::timestamptz cast is required — an untyped parameter minus an interval fails
// at runtime.
//
// Errors are logged; a partial/empty result just means less chronic suppression (the
// safe direction).
func queryFingerprintCounts(log *slog.Logger, db *sqlx.DB, accountID string, rootTime time.Time, fps []string, fpBucket map[string]string) map[string]int {
	sum7d := map[string]int{}
	if accountID == "" || len(fps) == 0 {
		return sum7d
	}
	const q = `
		SELECT fingerprint, count(*)
		FROM events
		WHERE cloud_account_id = $1::uuid
		  AND fingerprint = ANY($2)
		  AND starts_at >  $3::timestamptz - interval '7 days'
		  AND starts_at <= $3::timestamptz
		GROUP BY fingerprint`
	rows, err := db.Query(q, accountID, pq.Array(fps), rootTime)
	if err != nil {
		if log != nil {
			log.Warn("incident assembly: rarity query failed", "error", err, "account", accountID)
		}
		return sum7d
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var fp string
		var c int
		if err := rows.Scan(&fp, &c); err != nil {
			if log != nil {
				log.Warn("incident assembly: rarity row scan failed", "error", err, "account", accountID)
			}
			continue
		}
		sum7d[fpBucket[fp]] += c
	}
	if err := rows.Err(); err != nil {
		if log != nil {
			log.Warn("incident assembly: rarity scan failed", "error", err, "account", accountID)
		}
		return sum7d
	}
	return sum7d
}

// toTierItem builds the display item from a group's representative row; the caller
// fills OccurrenceCount and Sources from the collapse.
func toTierItem(r windowRow, rates map[string]triage.Rate) tierItem {
	item := tierItem{
		EventID:        r.id.ID,
		Title:          r.title,
		Subject:        triage.SubjectKey(r.id),
		AggregationKey: r.id.AggregationKey,
		Priority:       r.priority,
		DtSeconds:      r.id.TsOffsetS,
	}
	if !r.startsAt.IsZero() {
		item.StartsAt = r.startsAt.UTC().Format(time.RFC3339)
	}
	if rt, ok := rates[triage.RateKey(r.id)]; ok && (rt.Expected > 0 || rt.Observed > 0) {
		item.Evidence = &tierEvidence{ExpectedInWindow: rt.Expected, ObservedInWindow: rt.Observed}
	}
	return item
}
