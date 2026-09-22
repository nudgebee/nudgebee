package recommendation

import (
	"encoding/json"
	"fmt"
	"math"
	"nudgebee/services/internal/database"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// NFS category constants
const (
	NFSCategoryCost        = "cost"
	NFSCategorySecurity    = "security"
	NFSCategoryConfig      = "config"
	NFSCategoryPerformance = "performance"
)

// categoryCategoryMap maps DB recommendation categories to NFS categories.
var categoryCategoryMap = map[string]string{
	"RightSizing":                NFSCategoryCost,
	"K8sSpotRecommendation":      NFSCategoryCost,
	"Security":                   NFSCategorySecurity,
	"Configuration":              NFSCategoryConfig,
	"InfraUpgrade":               NFSCategoryPerformance,
	"WarehouseQueryOptimization": NFSCategoryCost,
}

// ruleCategoryOverrides maps specific rule_names to NFS categories
// when the default category mapping doesn't apply.
var ruleCategoryOverrides = map[string]string{
	// OOM / crash / upgrade rules → performance
	"pod_oom_killed":       NFSCategoryPerformance,
	"container_oom_killed": NFSCategoryPerformance,
	"crash_loop_back_off":  NFSCategoryPerformance,
	"eks_cluster_upgrade":  NFSCategoryPerformance,
	"aks_cluster_upgrade":  NFSCategoryPerformance,
	"gke_cluster_upgrade":  NFSCategoryPerformance,
	"node_not_ready":       NFSCategoryPerformance,
	"node_pressure":        NFSCategoryPerformance,
	"high_memory_usage":    NFSCategoryPerformance,
	"high_cpu_usage":       NFSCategoryPerformance,
	"disk_pressure":        NFSCategoryPerformance,
	"pid_pressure":         NFSCategoryPerformance,
	"network_unavailable":  NFSCategoryPerformance,

	// Explicit cost rules
	"abandoned_resource":           NFSCategoryCost,
	"unused_pvc":                   NFSCategoryCost,
	"pv_rightsize":                 NFSCategoryCost,
	"pod_right_sizing":             NFSCategoryCost,
	"replica-rightsizing":          NFSCategoryCost,
	"abandoned-resources":          NFSCategoryCost,
	"volume-rightsizing":           NFSCategoryCost,
	"vertical-rightsizing":         NFSCategoryCost,
	"Spot instance recommendation": NFSCategoryCost,

	// Explicit security/config rules
	"health_check": NFSCategoryConfig,
	"image_scan":   NFSCategorySecurity,
}

// autoFixableRules earn an effort boost because they can be auto-remediated.
var autoFixableRules = map[string]bool{
	"pod_right_sizing":     true,
	"replica-rightsizing":  true,
	"vertical-rightsizing": true,
	"volume-rightsizing":   true,
	"pv_rightsize":         true,
	"unused_pvc":           true,
	"health_check":         true,
}

// severityScores maps severity strings to numeric scores.
var severityScores = map[string]int{
	"Critical": 100,
	"High":     75,
	"Medium":   50,
	"Low":      25,
	"Info":     10,
}

// NFS v1 weighting constants. The score ranks recommendations on a single
// 0-100 "act on this first" scale, so these constants encode the
// cross-category exchange rate explicitly:
//
//   - Cost recs rank by measured dollars, not severity — severity is
//     unreliable there (live data holds Critical rows worth $0 and Medium
//     rows worth $578/mo).
//   - Performance findings (OOM kills, crashloops, node pressure) are active
//     damage and keep most of their severity weight.
//   - Config findings are latent risk with volume-inflated severities
//     (thousands of "High" findings per tenant), so they are dampened:
//     Critical config (70) ≈ a $600/mo cost rec, High config (53) ≈ $100/mo.
const (
	// savingsScoreCeiling is the $/mo at which the log savings curve saturates.
	savingsScoreCeiling = 5000.0

	securitySeverityWeight    = 1.00
	performanceSeverityWeight = 0.90
	configSeverityWeight      = 0.70

	costSavingsWeight  = 0.80
	costSeverityWeight = 0.20

	autoFixEffortBoost = 5
)

// GetNFSCategory returns the NFS category for a given recommendation
// category and rule name. Rule-level overrides take precedence.
func GetNFSCategory(category string, ruleName string) string {
	if override, ok := ruleCategoryOverrides[ruleName]; ok {
		return override
	}
	if cat, ok := categoryCategoryMap[category]; ok {
		return cat
	}
	return NFSCategoryConfig
}

func getSeverityScore(severity *string) int {
	if severity == nil {
		return 50
	}
	if score, ok := severityScores[*severity]; ok {
		return score
	}
	return 50
}

// getSavingsScore maps monthly savings onto 0-100 with a log curve:
// $2→12, $15→32, $150→58, $665→76, ≥$5K→100. The previous linear /500
// mapping zeroed out the typical rec (live p50 savings is ~$2/mo) while
// capping a $500 and a $50K rec at the same 100.
func getSavingsScore(savings float32) int {
	if savings <= 0 {
		return 0
	}
	score := 100 * math.Log1p(float64(savings)) / math.Log1p(savingsScoreCeiling)
	return int(math.Min(score, 100))
}

// getRecencyBoost gives genuinely new findings a small additive bump so they
// surface for triage without letting discovery volume own the ranking. In v0
// recency was 16-34% of the final score, which kept the top-N permanently
// equal to "whatever today's scan emitted".
func getRecencyBoost(createdAt *time.Time) int {
	if createdAt == nil {
		return 0
	}
	daysSince := time.Since(*createdAt).Hours() / 24
	switch {
	case daysSince < 1:
		return 8
	case daysSince < 7:
		return 5
	case daysSince < recencyBoostLastStepDays:
		return 2
	default:
		return 0
	}
}

// FinOpsScoreResult holds the computed score and metadata.
type FinOpsScoreResult struct {
	Score     int
	Band      string
	Breakdown map[string]any
}

// ComputeFinOpsScore calculates the NFS v1 score for a recommendation.
func ComputeFinOpsScore(category string, ruleName string, severity *string, estimatedSavings float32, createdAt *time.Time) FinOpsScoreResult {
	// Sanitize non-finite savings (storable in float columns) up front: NaN
	// poisons the score arithmetic and, worse, fails json.Marshal of the
	// breakdown — which aborts the caller's whole upsert batch.
	if math.IsNaN(float64(estimatedSavings)) || math.IsInf(float64(estimatedSavings), 0) {
		estimatedSavings = 0
	}

	nfsCategory := GetNFSCategory(category, ruleName)
	sevScore := getSeverityScore(severity)
	savingsScore := getSavingsScore(estimatedSavings)
	recencyBoost := getRecencyBoost(createdAt)

	// Cost recs with no positive savings are "increase resources" reliability
	// recommendations (e.g. an under-provisioned pod_right_sizing) — dollars
	// carry no signal there, so score them like performance findings.
	scoredAs := nfsCategory
	if nfsCategory == NFSCategoryCost && estimatedSavings <= 0 {
		scoredAs = NFSCategoryPerformance
	}

	var base float64
	switch scoredAs {
	case NFSCategoryCost:
		base = float64(savingsScore)*costSavingsWeight + float64(sevScore)*costSeverityWeight
	case NFSCategorySecurity:
		base = float64(sevScore) * securitySeverityWeight
	case NFSCategoryPerformance:
		base = float64(sevScore) * performanceSeverityWeight
	default: // config
		base = float64(sevScore) * configSeverityWeight
	}

	// Round, don't truncate: 100*0.70 is 69.999… in binary floating point.
	baseScore := int(math.Round(base))
	finalScore := baseScore + recencyBoost

	effortBoost := 0
	if autoFixableRules[ruleName] {
		effortBoost = autoFixEffortBoost
		finalScore += effortBoost
	}

	// Clamp 0-100
	if finalScore > 100 {
		finalScore = 100
	}
	if finalScore < 0 {
		finalScore = 0
	}

	band := GetBand(finalScore)

	sevStr := ""
	if severity != nil {
		sevStr = *severity
	}
	recencyDays := 0.0
	if createdAt != nil {
		recencyDays = time.Since(*createdAt).Hours() / 24
	}

	breakdown := map[string]any{
		"nfs_category": nfsCategory,
		"scored_as":    scoredAs,
		"base_score":   baseScore,
		"factors": map[string]any{
			"severity":          sevStr,
			"severity_score":    sevScore,
			"recency_days":      int(recencyDays),
			"recency_boost":     recencyBoost,
			"estimated_savings": estimatedSavings,
			"savings_score":     savingsScore,
		},
		"adjustments": map[string]any{
			"effort_boost": effortBoost,
		},
		"version": "v1",
	}

	return FinOpsScoreResult{
		Score:     finalScore,
		Band:      band,
		Breakdown: breakdown,
	}
}

// BandCooldowns defines the minimum interval between nudges for each band.
// Bands not present (Medium, Low) are never individually nudged.
var BandCooldowns = map[string]time.Duration{
	"Act Now":  24 * time.Hour,
	"Critical": 7 * 24 * time.Hour,
	"High":     30 * 24 * time.Hour,
}

// GetBand returns the NFS band label for a given score.
func GetBand(score int) string {
	switch {
	case score >= 90:
		return "Act Now"
	case score >= 75:
		return "Critical"
	case score >= 55:
		return "High"
	case score >= 35:
		return "Medium"
	default:
		return "Low"
	}
}

// UpdateFinOpsScoreForRecommendation computes and persists the finops score for a single recommendation by ID.
func UpdateFinOpsScoreForRecommendation(ctx *security.RequestContext, dbms *database.DatabaseManager, id string, category string, ruleName string, severity *string, estimatedSavings float32, createdAt *time.Time) error {
	result := ComputeFinOpsScore(category, ruleName, severity, estimatedSavings, createdAt)

	breakdownJSON, err := json.Marshal(result.Breakdown)
	if err != nil {
		ctx.GetLogger().Error("error marshalling finops score breakdown", "error", err)
		return err
	}

	_, err = dbms.Db.Exec(`
		UPDATE recommendation
		SET finops_score = $1, finops_band = $2, finops_score_breakdown = $3
		WHERE id = $4`,
		result.Score, result.Band, string(breakdownJSON), id)
	if err != nil {
		ctx.GetLogger().Error("error updating finops score", "error", err, "id", id)
		return err
	}
	return nil
}

// recencyBoostLastStepDays is the age at which getRecencyBoost reaches zero.
// The recompute's "can this row still change" predicate is derived from it, so
// the two cannot drift apart.
const recencyBoostLastStepDays = 30

// recencyRescoreGraceDays keeps a row in the daily walk for this long after
// its boost reached zero, so a run of missed daily passes (a tick skipped while
// a pass is in flight, a pod restart) still lands the final rewrite instead of
// leaving the row one step short forever.
const recencyRescoreGraceDays = 7

// RecomputeAllFinOpsScores recomputes scores for all open recommendations.
// Called by the finops-score-recompute cron every 6 hours. This is the only
// path that writes scores for existing rows — scanner upserts intentionally
// skip finops_* on conflict because they don't know the row's true created_at.
func RecomputeAllFinOpsScores(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}
	_, err = recomputeFinOpsScoresWith(ctx, dbms, 0)
	return err
}

// recRow is one Open recommendation as the recompute reads it. Only the class
// of a pod_right_sizing payload is kept, never the payload itself.
type recRow struct {
	id                string
	tenantID          string
	cloudAccountID    *string
	category          string
	ruleName          string
	severity          *string
	estimatedSavings  *float32
	createdAt         *time.Time
	resourceName      *string
	resourceNamespace *string
	resourceID        *string
	changeClass       ChangeClass
}

// scoreRow is a scored recommendation ready for the batched UPDATE.
type scoreRow struct {
	id        string
	score     int
	band      string
	breakdown string
}

// recomputeStats is what one pass reports. unchanged counts rows the UPDATE
// probed and left alone because their stored values already matched.
type recomputeStats struct {
	scanned     int
	updated     int
	unchanged   int
	writeFailed int
	errors      int
	resolved    int
	unresolved  int
	pages       int
}

// recomputeFinOpsScoresWith is the walk itself, on the given database so a
// test can point it at a scratch schema. Each keyset page is read, closed,
// scored, annotated and written before the next page is fetched, so memory is
// bounded by one page rather than by the size of the Open set, and no SELECT
// cursor is ever held open across the knowledge-graph queries annotation
// issues on this same pool.
func recomputeFinOpsScoresWith(ctx *security.RequestContext, dbms *database.DatabaseManager, window time.Duration) (recomputeStats, error) {
	tStart := time.Now()
	var stats recomputeStats
	var readDuration, annotateDuration, updateDuration time.Duration

	// Read the Open set in bounded keyset pages rather than one unbounded
	// statement. Paging on (created_at, id) rides idx_recommendation_open_created_at_id
	// (migration V749, partial on status='Open'), the same cursor pattern
	// runbook-server's WorkflowDao.FindNewRecommendations already uses, so each
	// page is an ordered range scan that short-circuits at LIMIT.
	//
	// readUntil pins the upper bound at start: rows created while the walk is in
	// flight are out of scope for this run (they were scored at insert time) and
	// pinning it makes termination independent of the insert rate. readSince is
	// the zero time for the full pass, which the same range predicate turns into
	// "every Open row", so both passes share one query shape and one index path.
	//
	// mutableSince bounds the recency boost: past recencyBoostLastStepDays the
	// boost is zero and stays zero, so a row older than that can only change if
	// it has a resource to re-annotate, or its scoring inputs moved since it was
	// last scored, or it was never scored. The WHERE below spells that out, so
	// the rows that dominate the Open set — image-scan findings with no resource
	// and no savings — drop out of the walk once their boost has decayed instead
	// of being read, scored and probed every day for nothing. The predicate is a
	// filter over the same index walk, not a second index.
	const readPageSize = 2000
	readUntil := time.Now()
	var readSince time.Time
	if window > 0 {
		readSince = readUntil.Add(-window)
	}
	mutableSince := readUntil.Add(-(recencyBoostLastStepDays + recencyRescoreGraceDays) * 24 * time.Hour)
	var (
		cursorCreatedAt time.Time
		cursorID        string
		hasCursor       bool
	)

	// resource_name + namespace are derived from the cloud_resourses join (the same
	// source the recommendations view uses), NOT the raw recommendation JSONB: that
	// JSONB's shape varies per rule type and usually omits the namespace entirely
	// (e.g. pod_right_sizing keys by workload name and carries no namespace at all),
	// so parsing it resolves nothing.
	//
	// The stored breakdown records the severity and savings the score was computed
	// from, so comparing those against the row is what detects a producer's
	// re-scan upsert having changed either. Every producer refreshes severity and
	// savings on conflict; none of them touches finops_*, and this check is what
	// keeps that contract from needing to change per producer.
	const recomputeSelect = `
		SELECT
			r.id, r.tenant_id, r.cloud_account_id, r.category, r.rule_name,
			r.severity, r.estimated_savings, r.created_at,
			cr.name AS resource_name,
			CASE
				WHEN cr.meta ->> 'namespace' IS NOT NULL THEN cr.meta ->> 'namespace'
				WHEN cr.meta -> 'config' ->> 'namespace' IS NOT NULL THEN cr.meta -> 'config' ->> 'namespace'
				WHEN r.recommendation -> 'spec' -> 'claimRef' ->> 'namespace' IS NOT NULL THEN r.recommendation -> 'spec' -> 'claimRef' ->> 'namespace'
				WHEN r.recommendation -> 'metadata' ->> 'namespace' IS NOT NULL THEN r.recommendation -> 'metadata' ->> 'namespace'
				ELSE r.recommendation ->> 'namespace'
			END AS resource_k8s_namespace,
			r.resource_id,
			CASE
				-- Only pod_right_sizing needs its payload (to read the request
				-- deltas for change classification); every other rule classifies
				-- from its name alone, and fetching JSONB for the whole Open set
				-- would balloon this scan's memory for nothing.
				WHEN r.rule_name = 'pod_right_sizing' THEN r.recommendation
			END AS change_payload
		FROM recommendation r
		LEFT JOIN cloud_resourses cr ON cr.id = r.resource_id
		WHERE r.status = 'Open' AND r.created_at > $1 AND r.created_at <= $2
		  AND (
		    r.resource_id IS NOT NULL
		    OR r.created_at > $3
		    OR r.finops_score_breakdown ->> 'version' IS NULL
		    OR COALESCE(r.finops_score_breakdown -> 'factors' ->> 'severity', '')
		       IS DISTINCT FROM COALESCE(r.severity, '')
		    OR COALESCE((r.finops_score_breakdown -> 'factors' ->> 'estimated_savings')::float4, 0)
		       IS DISTINCT FROM COALESCE(r.estimated_savings, 0)::float4
		  )`

	kgService := core.NewService(ctx, ctx.GetLogger(), dbms)
	impactCache := map[string]*core.ImpactSummary{}
	page := make([]recRow, 0, readPageSize)

	for {
		tRead := time.Now()
		var (
			rows *sqlx.Rows
			qerr error
		)
		if hasCursor {
			rows, qerr = dbms.Db.Queryx(recomputeSelect+`
			  AND (r.created_at, r.id) > ($4, $5::uuid)
			ORDER BY r.created_at ASC, r.id ASC
			LIMIT $6`, readSince, readUntil, mutableSince, cursorCreatedAt, cursorID, readPageSize)
		} else {
			rows, qerr = dbms.Db.Queryx(recomputeSelect+`
			ORDER BY r.created_at ASC, r.id ASC
			LIMIT $4`, readSince, readUntil, mutableSince, readPageSize)
		}
		if qerr != nil {
			ctx.GetLogger().Error("error querying recommendations for score recompute", "error", qerr)
			return stats, qerr
		}

		page = page[:0]
		pageCount := 0
		advanced := false
		for rows.Next() {
			var r recRow
			var createdAt time.Time
			var changePayload []byte
			pageCount++
			if err := rows.Scan(&r.id, &r.tenantID, &r.cloudAccountID, &r.category, &r.ruleName, &r.severity, &r.estimatedSavings, &createdAt, &r.resourceName, &r.resourceNamespace, &r.resourceID, &changePayload); err != nil {
				ctx.GetLogger().Error("error scanning recommendation row", "error", err)
				stats.errors++
				continue
			}
			r.createdAt = &createdAt
			// Classify here and keep only the class, so the payload bytes never
			// outlive the scan.
			r.changeClass = ClassifyChange(r.ruleName, changePayload)
			cursorCreatedAt, cursorID, hasCursor, advanced = createdAt, r.id, true, true
			page = append(page, r)
		}
		if err := rows.Err(); err != nil {
			ctx.GetLogger().Error("error iterating recommendation rows for score recompute", "error", err)
			if cerr := rows.Close(); cerr != nil {
				ctx.GetLogger().Error("error closing rows", "error", cerr)
			}
			return stats, err
		}
		if cerr := rows.Close(); cerr != nil {
			ctx.GetLogger().Error("error closing rows", "error", cerr)
		}
		readDuration += time.Since(tRead)
		stats.pages++
		stats.scanned += len(page)

		annotated, written, updated, writeFailed, errs := scoreAndWritePage(ctx, dbms, kgService, impactCache, page)
		annotateDuration += annotated
		updateDuration += written
		stats.updated += updated
		stats.writeFailed += writeFailed
		stats.errors += errs
		stats.unchanged += len(page) - errs - updated

		if pageCount < readPageSize {
			break
		}
		if !advanced {
			// Every row in a full page failed to scan, so the cursor did not move
			// and the next page would re-read the same rows forever. Bail out.
			return stats, fmt.Errorf("finops score recompute: no scannable rows in a full page of %d; aborting", readPageSize)
		}
	}

	for _, imp := range impactCache {
		if imp != nil {
			stats.resolved++
		} else {
			stats.unresolved++
		}
	}

	ctx.GetLogger().Info("finops score recompute complete",
		"window", window,
		"scanned", stats.scanned,
		"updated", stats.updated,
		"unchanged", stats.unchanged,
		"pages", stats.pages,
		"resources_resolved", stats.resolved,
		"resources_unresolved", stats.unresolved,
		"errors", stats.errors,
		"read_duration", readDuration,
		"annotate_duration", annotateDuration,
		"update_duration", updateDuration,
		"total_duration", time.Since(tStart),
	)
	return stats, nil
}

// scoreAndWritePage scores and blast-radius-annotates one page of rows and
// writes the results. Annotation resolves each recommendation to its
// knowledge-graph node — a k8s workload by (namespace, name), or a cloud
// resource by resource_id — and stamps a safety band into the breakdown JSONB.
// Results are memoized in cache per resource across pages, so each resource is
// resolved + traversed at most once per run. Returns the time spent scoring and
// writing, the rows the UPDATE changed, the rows whose write failed, and the
// error count (which includes the failed writes).
func scoreAndWritePage(ctx *security.RequestContext, dbms *database.DatabaseManager, kg *core.Service, cache map[string]*core.ImpactSummary, page []recRow) (annotateDuration, updateDuration time.Duration, updated, writeFailed, errCount int) {
	tAnnotate := time.Now()
	batch := make([]scoreRow, 0, len(page))
	for _, r := range page {
		savings := float32(0)
		if r.estimatedSavings != nil {
			savings = *r.estimatedSavings
		}
		result := ComputeFinOpsScore(r.category, r.ruleName, r.severity, savings, r.createdAt)

		accountID := ""
		if r.cloudAccountID != nil {
			accountID = *r.cloudAccountID
		}
		// Identity comes from the cloud_resourses join; annotate no-ops when it
		// resolves to no graph node (k8s workload or cloud resource absent from the
		// graph, or an account-level rec with a null resource_id).
		ns, name, resID := "", "", ""
		if r.resourceNamespace != nil {
			ns = *r.resourceNamespace
		}
		if r.resourceName != nil {
			name = *r.resourceName
		}
		if r.resourceID != nil {
			resID = *r.resourceID
		}
		annotateBreakdownWithImpact(kg, r.tenantID, accountID, ns, name, resID, r.changeClass, result.Breakdown, cache)

		breakdownJSON, err := json.Marshal(result.Breakdown)
		if err != nil {
			errCount++
			continue
		}
		batch = append(batch, scoreRow{
			id:        r.id,
			score:     result.Score,
			band:      result.Band,
			breakdown: string(breakdownJSON),
		})
	}
	annotateDuration = time.Since(tAnnotate)

	// Batch update using unnest — one statement per chunk. The IS DISTINCT FROM
	// guard turns a rewrite of already-stored values into a read, so a revisit
	// leaves no dead tuples. Two breakdown fields are masked out of the
	// comparison because they change on every computation without the score
	// having moved: impact_summary.computed_at is stamped with now() on every
	// annotation, and factors.recency_days is the row's age in whole days, which
	// ticks over daily for every row and would otherwise turn the daily pass
	// into a rewrite of the whole Open set. Nothing reads either back; the
	// stored recency_days is therefore only current as of the row's last real
	// change, and factors.recency_boost — which is what moves the score — stays
	// in the comparison.
	tUpdate := time.Now()
	const batchSize = 500
	for i := 0; i < len(batch); i += batchSize {
		end := i + batchSize
		if end > len(batch) {
			end = len(batch)
		}
		chunk := batch[i:end]

		ids := make([]string, len(chunk))
		scores := make([]int, len(chunk))
		bands := make([]string, len(chunk))
		breakdowns := make([]string, len(chunk))
		for j, row := range chunk {
			ids[j] = row.id
			scores[j] = row.score
			bands[j] = row.band
			breakdowns[j] = row.breakdown
		}

		res, err := dbms.Db.Exec(`
			UPDATE recommendation AS r
			SET finops_score = v.score,
			    finops_band = v.band,
			    finops_score_breakdown = v.breakdown::jsonb
			FROM unnest($1::uuid[], $2::int[], $3::text[], $4::text[])
			    AS v(id, score, band, breakdown)
			WHERE r.id = v.id
			  AND (r.finops_score IS DISTINCT FROM v.score
			    OR r.finops_band IS DISTINCT FROM v.band
			    OR (r.finops_score_breakdown #- '{impact_summary,computed_at}' #- '{factors,recency_days}')
			       IS DISTINCT FROM (v.breakdown::jsonb #- '{impact_summary,computed_at}' #- '{factors,recency_days}'))`,
			pq.Array(ids), pq.Array(scores), pq.Array(bands), pq.Array(breakdowns))
		if err != nil {
			ctx.GetLogger().Error("error batch updating finops scores", "error", err, "batch_start", i)
			errCount += len(chunk)
			writeFailed += len(chunk)
			continue
		}
		if n, err := res.RowsAffected(); err == nil {
			updated += int(n)
		} else {
			updated += len(chunk)
		}
	}
	updateDuration = time.Since(tUpdate)
	return annotateDuration, updateDuration, updated, writeFailed, errCount
}

// ComputeAndSetFinOpsScoreFields calculates the finops score and returns the values
// to include in a recommendation upsert data map.
func ComputeAndSetFinOpsScoreFields(data map[string]any) {
	category, _ := data["category"].(string)
	ruleName, _ := data["rule_name"].(string)

	var severity *string
	if s, ok := data["severity"].(string); ok {
		severity = &s
	}

	var estimatedSavings float32
	switch v := data["estimated_savings"].(type) {
	case float32:
		estimatedSavings = v
	case float64:
		estimatedSavings = float32(v)
	case int:
		estimatedSavings = float32(v)
	}

	var createdAt *time.Time
	if t, ok := data["created_at"].(time.Time); ok {
		createdAt = &t
	} else {
		// Scanner payloads carry no created_at; now() is correct for the INSERT
		// case (the DB defaults created_at to now()). Re-upserts of existing rows
		// no longer overwrite finops_* on conflict, so this fresh-recency score
		// never lands on old rows — the 6h recompute cron refreshes those from
		// the true created_at.
		now := time.Now()
		createdAt = &now
	}

	result := ComputeFinOpsScore(category, ruleName, severity, estimatedSavings, createdAt)
	breakdownJSON, err := json.Marshal(result.Breakdown)
	if err != nil {
		return
	}

	data["finops_score"] = result.Score
	data["finops_band"] = result.Band
	data["finops_score_breakdown"] = string(breakdownJSON)
}
