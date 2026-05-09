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

	// Service tier constants
	TierCustomerFacing = 0 // Ingress, gateway, frontend
	TierCoreInfra      = 1 // Databases, queues
	TierDefault        = 2 // Business services
	TierMonitoring     = 3 // Observability, logging

	// Tier bonus: (4 - tier) * 10
	TierBonusMultiplier = 10
)

// Service tier pattern matching slices (package-level for performance)
var (
	tier0Patterns   = []string{"ingress", "gateway", "nginx-controller", "frontend", "loadbalancer", "haproxy", "traefik", "envoy"}
	tier1Patterns   = []string{"postgres", "mysql", "mongodb", "redis", "rabbitmq", "kafka", "elasticsearch", "cassandra", "mariadb", "memcached", "etcd", "zookeeper", "cockroach", "clickhouse"}
	tier3Patterns   = []string{"prometheus", "grafana", "victoria", "loki", "tempo", "jaeger", "otel", "opentelemetry", "fluent", "filebeat", "logstash", "alertmanager", "thanos", "cortex", "mimir"}
	tier3Namespaces = []string{"monitoring", "observability", "logging", "metrics", "tracing"}
)

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

	// 8. Get service tier and calculate tier bonus
	serviceTier := TierDefault
	tierBonus := 0
	if event.SubjectOwner != nil && event.CloudAccountId != nil {
		namespace := ""
		if event.SubjectNamespace != nil {
			namespace = *event.SubjectNamespace
		}
		serviceTier = getServiceTier(ctx, db, *event.SubjectOwner, namespace, *event.CloudAccountId)
		tierBonus = (4 - serviceTier) * TierBonusMultiplier
		factorCount++
	}
	factors["service_tier"] = serviceTier
	factors["tier_bonus"] = tierBonus

	// Calculate final score
	totalAdjustments := -duplicatePenalty + correlationAdj + findingTypeAdj + evidenceBonus + tierBonus
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

// getServiceTier determines the service tier based on workload characteristics
// Tier 0: Customer-facing (ingress, gateway, frontend)
// Tier 1: Core infrastructure (databases, queues)
// Tier 2: Default (business services)
// Tier 3: Monitoring/observability
func getServiceTier(ctx context.Context, db *sqlx.DB, subjectOwner, namespace, cloudAccountID string) int {
	nameLower := strings.ToLower(subjectOwner)
	namespaceLower := strings.ToLower(namespace)

	// Tier 0: Customer-facing patterns
	for _, pattern := range tier0Patterns {
		if strings.Contains(nameLower, pattern) {
			return TierCustomerFacing
		}
	}

	// Tier 1: Core infrastructure (databases, queues)
	for _, pattern := range tier1Patterns {
		if strings.Contains(nameLower, pattern) {
			return TierCoreInfra
		}
	}

	// Tier 3: Monitoring/observability patterns
	for _, pattern := range tier3Patterns {
		if strings.Contains(nameLower, pattern) {
			return TierMonitoring
		}
	}

	// Tier 3: Monitoring namespaces
	for _, ns := range tier3Namespaces {
		if strings.Contains(namespaceLower, ns) {
			return TierMonitoring
		}
	}

	// Query k8s_workloads for additional context (labels, component)
	query := `
		SELECT
			COALESCE(labels->>'app.kubernetes.io/component', '') as component,
			COALESCE(labels->>'app.kubernetes.io/name', '') as app_name
		FROM k8s_workloads
		WHERE cloud_account_id = $1
		  AND name = $2
		  AND ($3 = '' OR namespace = $3)
		LIMIT 1
	`

	var workload struct {
		Component string `db:"component"`
		AppName   string `db:"app_name"`
	}

	err := db.GetContext(ctx, &workload, query, cloudAccountID, subjectOwner, namespace)
	if err == nil {
		componentLower := strings.ToLower(workload.Component)
		appNameLower := strings.ToLower(workload.AppName)

		// Check component for tier patterns
		for _, pattern := range tier0Patterns {
			if strings.Contains(componentLower, pattern) || strings.Contains(appNameLower, pattern) {
				return TierCustomerFacing
			}
		}
		for _, pattern := range tier1Patterns {
			if strings.Contains(componentLower, pattern) || strings.Contains(appNameLower, pattern) {
				return TierCoreInfra
			}
		}
		for _, pattern := range tier3Patterns {
			if strings.Contains(componentLower, pattern) || strings.Contains(appNameLower, pattern) {
				return TierMonitoring
			}
		}
	}

	// Default: Tier 2 (business services)
	return TierDefault
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
