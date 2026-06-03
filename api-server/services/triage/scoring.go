package triage

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
)

// ScoreResult contains the computed score and metadata
type ScoreResult struct {
	Score      int                    `json:"score"`      // 0-100
	Priority   string                 `json:"priority"`   // P0, P1, P2, P3
	Factors    map[string]interface{} `json:"factors"`    // Breakdown for explainability
	Confidence float64                `json:"confidence"` // 0.0-1.0
}

// Scoring constants
const (
	// Base severity values (0-25 scale)
	SeverityHigh   = 25
	SeverityMedium = 15
	SeverityLow    = 10
	SeverityInfo   = 5
	SeverityDebug  = 3

	// Environment multipliers
	EnvMultiplierProd    = 1.0
	EnvMultiplierNonProd = 0.3
	EnvMultiplierDefault = 0.5

	// Duplicate penalty
	DuplicatePenaltyPerOccurrence = 5
	MaxDuplicatePenalty           = 30

	// Correlation adjustments
	CorrelationBonusRootCause     = 15
	CorrelationPenaltyDownstream  = -10
	CorrelationPenaltyUpstream    = -10
	CorrelationPenaltySameService = -5

	// Finding type adjustments
	FindingTypeBonusSLO            = 10
	FindingTypeBonusAnomaly        = 5
	FindingTypePenaltyConfigChange = -10

	// Evidence bonuses
	EvidenceBonusOOMKilled = 15
	EvidenceBonusRestarts  = 10
	RestartThreshold       = 3

	// Score multiplier (to scale 0-25 base to 0-100)
	ScoreMultiplier = 4
)

// Service-tier bonuses are not computed here. They are seeded as
// system-default rows in `event_triage_rules` (rule_type='scoring',
// action='adjust_score') and applied by processor.go after ComputeScore.
// Tenants edit or override them via the existing triage rules UI.

// ComputeScore calculates the priority score for an event
func ComputeScore(ctx context.Context, db *sqlx.DB, event *models.Event) (*ScoreResult, error) {
	factors := make(map[string]interface{})
	var factorCount int

	// 1. Get base severity from event.priority
	baseSeverity := getBaseSeverity(event.Priority)
	factors["base_severity"] = baseSeverity
	factors["base_severity_source"] = "event.priority"
	factorCount++

	// 2. Get environment multiplier from cloud_accounts.account_env
	envMultiplier := EnvMultiplierDefault
	if event.CloudAccountId != nil {
		envMultiplier = getEnvironmentMultiplier(ctx, db, *event.CloudAccountId)
	}
	factors["env_multiplier"] = envMultiplier
	factorCount++

	// 3. Calculate raw score
	rawScore := float64(baseSeverity) * envMultiplier * ScoreMultiplier
	factors["raw_score"] = rawScore

	// 4. Get duplicate penalty
	duplicatePenalty := 0
	if event.Id != "" {
		duplicatePenalty = getDuplicatePenalty(ctx, db, event.Id)
	}
	factors["duplicate_penalty"] = duplicatePenalty
	if duplicatePenalty != 0 {
		factorCount++
	}

	// 5. Get correlation adjustment
	correlationAdj := 0
	correlationType := ""
	if event.Id != "" {
		correlationAdj, correlationType = getCorrelationAdjustment(ctx, db, event.Id)
	}
	factors["correlation_adjustment"] = correlationAdj
	factors["correlation_type"] = correlationType
	if correlationAdj != 0 {
		factorCount++
	}

	// 6. Get finding type adjustment
	findingTypeAdj := getFindingTypeAdjustment(event.FindingType)
	factors["finding_type_adjustment"] = findingTypeAdj
	if findingTypeAdj != 0 {
		factorCount++
	}

	// 7. Get evidence bonus
	evidenceBonus := getEvidenceBonus(event.Evidences)
	factors["evidence_bonus"] = evidenceBonus
	if evidenceBonus != 0 {
		factorCount++
	}

	// Service-tier bonus is applied later by processor.go via
	// event_triage_rules (rule_type='scoring').

	// Calculate final score
	totalAdjustments := -duplicatePenalty + correlationAdj + findingTypeAdj + evidenceBonus
	factors["total_adjustments"] = totalAdjustments

	finalScore := int(rawScore) + totalAdjustments
	finalScore = clamp(finalScore, 0, 100)
	factors["final_score"] = finalScore

	// Map score to priority
	priority := scoreToPriority(finalScore)
	factors["priority"] = priority

	// Calculate confidence based on available data
	confidence := calculateConfidence(factorCount, envMultiplier)
	factors["confidence"] = confidence

	slog.DebugContext(ctx, "Computed event score",
		"event_id", event.Id,
		"score", finalScore,
		"priority", priority,
		"factors", factors,
	)

	return &ScoreResult{
		Score:      finalScore,
		Priority:   priority,
		Factors:    factors,
		Confidence: confidence,
	}, nil
}

// getBaseSeverity maps event.priority to a base severity score (0-25)
func getBaseSeverity(priority *string) int {
	if priority == nil {
		return SeverityLow // Default
	}

	switch strings.ToUpper(*priority) {
	case "HIGH":
		return SeverityHigh
	case "MEDIUM":
		return SeverityMedium
	case "LOW":
		return SeverityLow
	case "INFO":
		return SeverityInfo
	case "DEBUG":
		return SeverityDebug
	default:
		return SeverityLow
	}
}

// getEnvironmentMultiplier fetches the environment multiplier from cloud_accounts
func getEnvironmentMultiplier(ctx context.Context, db *sqlx.DB, cloudAccountID string) float64 {
	query := `SELECT account_env FROM cloud_accounts WHERE id = $1`

	var accountEnv string
	err := db.GetContext(ctx, &accountEnv, query, cloudAccountID)
	if err != nil {
		slog.DebugContext(ctx, "Failed to get account_env, using default",
			"cloud_account_id", cloudAccountID,
			"error", err,
		)
		return EnvMultiplierDefault
	}

	switch strings.ToLower(accountEnv) {
	case "prod":
		return EnvMultiplierProd
	case "non_prod", "non-prod":
		return EnvMultiplierNonProd
	default:
		return EnvMultiplierDefault
	}
}

// getDuplicatePenalty calculates the penalty based on occurrence number
func getDuplicatePenalty(ctx context.Context, db *sqlx.DB, eventID string) int {
	query := `
		SELECT occurrence_number
		FROM event_duplicates
		WHERE event_id = $1
	`

	var occurrenceNumber int
	err := db.GetContext(ctx, &occurrenceNumber, query, eventID)
	if err != nil {
		// No duplicate record means first occurrence
		return 0
	}

	if occurrenceNumber <= 1 {
		return 0
	}

	// Penalty = (occurrence - 1) * penalty_per_occurrence, capped at max
	penalty := (occurrenceNumber - 1) * DuplicatePenaltyPerOccurrence
	if penalty > MaxDuplicatePenalty {
		penalty = MaxDuplicatePenalty
	}

	return penalty
}

// getCorrelationAdjustment calculates the score adjustment based on correlation type
func getCorrelationAdjustment(ctx context.Context, db *sqlx.DB, eventID string) (int, string) {
	query := `
		SELECT correlation_type, correlation_score
		FROM event_correlations
		WHERE event_id = $1
		ORDER BY correlation_score DESC
		LIMIT 1
	`

	var correlation struct {
		CorrelationType  string  `db:"correlation_type"`
		CorrelationScore float64 `db:"correlation_score"`
	}

	err := db.GetContext(ctx, &correlation, query, eventID)
	if err != nil {
		// No correlation record
		return 0, ""
	}

	// Only apply adjustment if correlation score is above threshold
	if correlation.CorrelationScore < 0.5 {
		return 0, correlation.CorrelationType
	}

	switch correlation.CorrelationType {
	case "likely_root_cause":
		return CorrelationBonusRootCause, correlation.CorrelationType
	case "downstream_impact":
		return CorrelationPenaltyDownstream, correlation.CorrelationType
	case "upstream_dependency":
		return CorrelationPenaltyUpstream, correlation.CorrelationType
	case "same_service":
		return CorrelationPenaltySameService, correlation.CorrelationType
	default:
		return 0, correlation.CorrelationType
	}
}

// getFindingTypeAdjustment calculates the score adjustment based on finding type
func getFindingTypeAdjustment(findingType *string) int {
	if findingType == nil {
		return 0
	}

	switch strings.ToLower(*findingType) {
	case "slo":
		return FindingTypeBonusSLO
	case "anomaly":
		return FindingTypeBonusAnomaly
	case "configuration_change":
		return FindingTypePenaltyConfigChange
	default:
		return 0
	}
}

// getEvidenceBonus extracts bonus points from event evidences
func getEvidenceBonus(evidences *models.Json) int {
	if evidences == nil {
		return 0
	}

	// Check if evidences is an array
	if !evidences.IsArray() {
		return 0
	}

	bonus := 0

	// Iterate through evidences array
	for _, item := range evidences.Array() {
		evidence, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		evidenceType, ok := evidence["type"].(string)
		if !ok {
			continue
		}

		if evidenceType == "pod_enricher" {
			data, ok := evidence["data"].(map[string]interface{})
			if !ok {
				continue
			}

			// Check for OOMKilled
			if lastStatus, ok := data["lastStatus"].(map[string]interface{}); ok {
				if reason, ok := lastStatus["reason"].(string); ok {
					if strings.Contains(strings.ToLower(reason), "oomkill") {
						bonus += EvidenceBonusOOMKilled
					}
				}
			}

			// Check for high restart count
			if restarts, ok := data["restarts"].(float64); ok {
				if int(restarts) >= RestartThreshold {
					bonus += EvidenceBonusRestarts
				}
			}
		}
	}

	return bonus
}

// scoreToPriority maps a score (0-100) to a priority level (P0-P3)
func scoreToPriority(score int) string {
	switch {
	case score >= 80:
		return "P0" // Critical
	case score >= 60:
		return "P1" // High
	case score >= 40:
		return "P2" // Medium
	default:
		return "P3" // Low
	}
}

// priorityToMinScore returns the minimum score for a given priority level.
// Used when manually correcting priority to keep score and priority in sync.
func priorityToMinScore(priority string) int {
	switch strings.ToUpper(priority) {
	case "P0":
		return 80
	case "P1":
		return 60
	case "P2":
		return 40
	default:
		return 20
	}
}

// calculateConfidence estimates confidence based on available data
func calculateConfidence(factorCount int, envMultiplier float64) float64 {
	// Start with base confidence
	confidence := 0.5

	// More factors = higher confidence
	confidence += float64(factorCount) * 0.1

	// Known environment = higher confidence
	if envMultiplier != EnvMultiplierDefault {
		confidence += 0.1
	}

	// Cap at 1.0
	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// clamp restricts a value to a range
func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// UpdateEventScore updates the event with the computed score
func UpdateEventScore(ctx context.Context, db *sqlx.DB, eventID string, result *ScoreResult) error {
	factorsJSON, err := json.Marshal(result.Factors)
	if err != nil {
		return err
	}

	query := `
		UPDATE events
		SET computed_score = $1,
		    computed_priority = $2,
		    score_factors = $3,
		    score_confidence = $4,
		    updated_at = NOW()
		WHERE id = $5
	`

	_, err = db.ExecContext(ctx, query, result.Score, result.Priority, factorsJSON, result.Confidence, eventID)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to update event score",
			"event_id", eventID,
			"error", err,
		)
		return err
	}

	slog.InfoContext(ctx, "Updated event score",
		"event_id", eventID,
		"score", result.Score,
		"priority", result.Priority,
	)

	return nil
}
