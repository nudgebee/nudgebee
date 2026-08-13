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
	case daysSince < 30:
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

// RecomputeAllFinOpsScores recomputes scores for all open recommendations.
// Called by the finops-score-recompute cron every 6 hours. This is the only
// path that writes scores for existing rows — scanner upserts intentionally
// skip finops_* on conflict because they don't know the row's true created_at.
func RecomputeAllFinOpsScores(ctx *security.RequestContext) error {
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Collect all computed scores in memory for batch update
	type scoreRow struct {
		id        string
		score     int
		band      string
		breakdown string
	}
	var batch []scoreRow

	// The scan loop below does nothing but read rows, so each SELECT's cursor is
	// released as soon as its last row lands. Scoring and blast-radius annotation
	// run afterwards, in a second pass: annotation issues its own knowledge-graph
	// queries on this same pool, and doing that from inside `rows.Next()` pinned
	// the connection for the whole recompute — Postgres bills time blocked on a
	// slow client to the statement, so this SELECT was logging multi-minute
	// durations against the slow-query alert while the query itself is a PK-keyed
	// join over an indexed `status = 'Open'` scan.
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
	}
	var recs []recRow

	errCount := 0
	// Read the Open set in bounded keyset pages rather than one unbounded
	// statement. The cron still scores every Open row, but a single ~180k-row
	// SELECT ran ~69s — long enough to trip the slow-query alert and to pin one
	// snapshot open across the whole read, holding back vacuum on a table this
	// size. Paging on (created_at, id) rides idx_recommendation_open_created_at_id
	// (migration V749, partial on status='Open'), the same cursor pattern
	// runbook-server's WorkflowDao.FindNewRecommendations already uses, so each
	// page is an ordered range scan that short-circuits at LIMIT.
	//
	// readUntil pins the upper bound at start: rows created while the walk is in
	// flight are out of scope for this run (they were scored at insert time) and
	// pinning it makes termination independent of the insert rate.
	const readPageSize = 2000
	readUntil := time.Now()
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
			r.resource_id
		FROM recommendation r
		LEFT JOIN cloud_resourses cr ON cr.id = r.resource_id
		WHERE r.status = 'Open' AND r.created_at <= $1`

	for {
		var (
			rows *sqlx.Rows
			qerr error
		)
		if hasCursor {
			rows, qerr = dbms.Db.Queryx(recomputeSelect+`
			  AND (r.created_at, r.id) > ($2, $3::uuid)
			ORDER BY r.created_at ASC, r.id ASC
			LIMIT $4`, readUntil, cursorCreatedAt, cursorID, readPageSize)
		} else {
			rows, qerr = dbms.Db.Queryx(recomputeSelect+`
			ORDER BY r.created_at ASC, r.id ASC
			LIMIT $2`, readUntil, readPageSize)
		}
		if qerr != nil {
			ctx.GetLogger().Error("error querying recommendations for score recompute", "error", qerr)
			return qerr
		}

		pageCount := 0
		advanced := false
		for rows.Next() {
			var r recRow
			var createdAt time.Time
			pageCount++
			if err := rows.Scan(&r.id, &r.tenantID, &r.cloudAccountID, &r.category, &r.ruleName, &r.severity, &r.estimatedSavings, &createdAt, &r.resourceName, &r.resourceNamespace, &r.resourceID); err != nil {
				ctx.GetLogger().Error("error scanning recommendation row", "error", err)
				errCount++
				continue
			}
			r.createdAt = &createdAt
			cursorCreatedAt, cursorID, hasCursor, advanced = createdAt, r.id, true, true
			recs = append(recs, r)
		}
		if err := rows.Err(); err != nil {
			ctx.GetLogger().Error("error iterating recommendation rows for score recompute", "error", err)
			if cerr := rows.Close(); cerr != nil {
				ctx.GetLogger().Error("error closing rows", "error", cerr)
			}
			return err
		}
		if cerr := rows.Close(); cerr != nil {
			ctx.GetLogger().Error("error closing rows", "error", cerr)
		}

		if pageCount < readPageSize {
			break
		}
		if !advanced {
			// Every row in a full page failed to scan, so the cursor did not move
			// and the next page would re-read the same rows forever. Bail out.
			return fmt.Errorf("finops score recompute: no scannable rows in a full page of %d; aborting", readPageSize)
		}
	}

	// Blast-radius annotation: resolve each recommendation to its knowledge-graph
	// node — a k8s workload by (namespace, name), or a cloud resource by resource_id —
	// and stamp a safety band into the breakdown JSONB. Always on; cost is bounded
	// (recs whose resource isn't in the graph are skipped, and results are memoized
	// per resource so each is resolved + traversed at most once per run).
	kgService := core.NewService(ctx, ctx.GetLogger(), dbms)
	impactCache := map[string]*recommendationImpact{}

	for _, r := range recs {
		savings := float32(0)
		if r.estimatedSavings != nil {
			savings = *r.estimatedSavings
		}
		result := ComputeFinOpsScore(r.category, r.ruleName, r.severity, savings, r.createdAt)

		accountID := ""
		if r.cloudAccountID != nil {
			accountID = *r.cloudAccountID
		}
		// Identity comes from the cloud_resourses join above; annotate no-ops when it
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
		annotateBreakdownWithImpact(kgService, r.tenantID, accountID, ns, name, resID, result.Breakdown, impactCache)

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

	// Batch update using unnest — single query for all rows
	const batchSize = 500
	updated := 0
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

		_, err := dbms.Db.Exec(`
			UPDATE recommendation AS r
			SET finops_score = v.score,
			    finops_band = v.band,
			    finops_score_breakdown = v.breakdown::jsonb
			FROM unnest($1::uuid[], $2::int[], $3::text[], $4::text[])
			    AS v(id, score, band, breakdown)
			WHERE r.id = v.id`,
			pq.Array(ids), pq.Array(scores), pq.Array(bands), pq.Array(breakdowns))
		if err != nil {
			ctx.GetLogger().Error("error batch updating finops scores", "error", err, "batch_start", i)
			errCount += len(chunk)
			continue
		}
		updated += len(chunk)
	}

	ctx.GetLogger().Info("finops score recompute complete", "updated", updated, "errors", errCount)
	return nil
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
