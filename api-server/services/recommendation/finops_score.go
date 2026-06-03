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

func getRecencyScore(createdAt *time.Time) int {
	if createdAt == nil {
		return 40
	}
	daysSince := time.Since(*createdAt).Hours() / 24
	switch {
	case daysSince < 1:
		return 100
	case daysSince < 7:
		return 70
	case daysSince < 30:
		return 40
	case daysSince < 90:
		return 20
	default:
		return 10
	}
}

func getSavingsScore(savings float32) int {
	if savings <= 0 {
		return 0
	}
	score := float64(savings) / 500.0 * 100
	return int(math.Min(score, 100))
}

// FinOpsScoreResult holds the computed score and metadata.
type FinOpsScoreResult struct {
	Score     int
	Band      string
	Breakdown map[string]any
}

// ComputeFinOpsScore calculates the NFS v0 score for a recommendation.
func ComputeFinOpsScore(category string, ruleName string, severity *string, estimatedSavings float32, createdAt *time.Time) FinOpsScoreResult {
	nfsCategory := GetNFSCategory(category, ruleName)
	sevScore := getSeverityScore(severity)
	recencyScore := getRecencyScore(createdAt)
	savingsScore := getSavingsScore(estimatedSavings)

	// Universal context (40%): severity 60% + recency 40%
	universalScore := int(float64(sevScore)*0.60 + float64(recencyScore)*0.40)

	// Category-specific (60%)
	var categoryScore int
	switch nfsCategory {
	case NFSCategoryCost:
		categoryScore = int(float64(savingsScore)*0.70 + float64(sevScore)*0.30)
	case NFSCategorySecurity:
		categoryScore = int(float64(sevScore)*0.80 + float64(recencyScore)*0.20)
	case NFSCategoryConfig:
		categoryScore = int(float64(sevScore)*0.70 + float64(recencyScore)*0.30)
	case NFSCategoryPerformance:
		categoryScore = int(float64(sevScore)*0.60 + float64(recencyScore)*0.40)
	default:
		categoryScore = sevScore
	}

	finalScore := int(float64(universalScore)*0.40 + float64(categoryScore)*0.60)

	// Effort boost for auto-fixable rules
	effortBoost := 0
	if autoFixableRules[ruleName] {
		effortBoost = 5
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
		"nfs_category":    nfsCategory,
		"universal_score": universalScore,
		"category_score":  categoryScore,
		"factors": map[string]any{
			"severity":          sevStr,
			"severity_score":    sevScore,
			"recency_days":      int(recencyDays),
			"recency_score":     recencyScore,
			"estimated_savings": estimatedSavings,
			"savings_score":     savingsScore,
		},
		"adjustments": map[string]any{
			"effort_boost": effortBoost,
		},
		"version": "v0",
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
// Called by the finops-score-recompute cron every 6 hours.
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
		// New recommendations won't have created_at in the data map
		// (DB defaults to now()), so use current time for accurate recency scoring.
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
