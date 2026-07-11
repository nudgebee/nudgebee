package recommendation

import (
	"encoding/json"
	"math"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
	"time"

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

	rows, err := dbms.Db.Queryx(`
		SELECT id, category, rule_name, severity, estimated_savings, created_at
		FROM recommendation
		WHERE status = 'Open'`)
	if err != nil {
		ctx.GetLogger().Error("error querying recommendations for score recompute", "error", err)
		return err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			ctx.GetLogger().Error("error closing rows", "error", cerr)
		}
	}()

	// Collect all computed scores in memory for batch update
	type scoreRow struct {
		id        string
		score     int
		band      string
		breakdown string
	}
	var batch []scoreRow

	errCount := 0
	for rows.Next() {
		var (
			id               string
			category         string
			ruleName         string
			severity         *string
			estimatedSavings *float32
			createdAt        *time.Time
		)
		if err := rows.Scan(&id, &category, &ruleName, &severity, &estimatedSavings, &createdAt); err != nil {
			ctx.GetLogger().Error("error scanning recommendation row", "error", err)
			errCount++
			continue
		}

		savings := float32(0)
		if estimatedSavings != nil {
			savings = *estimatedSavings
		}
		result := ComputeFinOpsScore(category, ruleName, severity, savings, createdAt)
		breakdownJSON, err := json.Marshal(result.Breakdown)
		if err != nil {
			errCount++
			continue
		}

		batch = append(batch, scoreRow{
			id:        id,
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
