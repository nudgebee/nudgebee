package triage

import (
	"encoding/json"
	"time"
)

// NB Status constants - Nudgebee triage status (separate from source status)
const (
	NBStatusOpen           = "OPEN"
	NBStatusAcknowledged   = "ACKNOWLEDGED"  // Deprecated: kept for backwards compatibility
	NBStatusInvestigating  = "INVESTIGATING" // Deprecated: kept for backwards compatibility
	NBStatusActionRequired = "ACTION_REQUIRED"
	NBStatusSnoozed        = "SNOOZED"
	NBStatusSuppressed     = "SUPPRESSED"
	NBStatusDropped        = "DROPPED"
	NBStatusDuplicate      = "DUPLICATE"
	NBStatusResolved       = "RESOLVED"
	NBStatusNoChange       = "NO_CHANGE" // Special preview status: scoring rules don't change status
)

// Classification type constants
const (
	ClassificationTruePositive   = "true_positive"
	ClassificationFalsePositive  = "false_positive"
	ClassificationBenignPositive = "benign_positive"
	ClassificationDuplicate      = "duplicate"
)

// Apply scope constants
const (
	ApplyScopeThisEvent       = "this_event"
	ApplyScopeThisFingerprint = "this_fingerprint"
	ApplyScopeTimeLimited     = "time_limited"
)

// Rule type constants
const (
	RuleTypeSuppression    = "suppression"
	RuleTypeScoring        = "scoring"
	RuleTypeClassification = "classification"
)

// Rule action constants
const (
	ActionSuppress              = "suppress"
	ActionDrop                  = "drop"
	ActionAdjustScore           = "adjust_score"
	ActionAutoClassifyDuplicate = "auto_classify_duplicate"
	ActionAutoClassifyFP        = "auto_classify_fp"
)

// Priority direction constants
const (
	PriorityDirectionTooHigh = "too_high"
	PriorityDirectionCorrect = "correct"
	PriorityDirectionTooLow  = "too_low"
)

// Reason codes for each classification type
var ValidReasonCodes = map[string][]string{
	ClassificationTruePositive: {
		"correct_severity",
		"wrong_service_tier",
		"missing_dependency",
	},
	ClassificationFalsePositive: {
		"known_noise",
		"threshold_too_sensitive",
		"test_alert",
		"wrong_environment",
	},
	ClassificationBenignPositive: {
		"maintenance_window",
		"expected_behavior",
		"batch_job",
		"deployment",
	},
	ClassificationDuplicate: {
		"duplicate_incident",
	},
}

// EventClassification represents a user classification of an event
type EventClassification struct {
	ID                string     `json:"id" db:"id"`
	EventID           string     `json:"event_id" db:"event_id"`
	CloudAccountID    string     `json:"cloud_account_id" db:"cloud_account_id"`
	TenantID          string     `json:"tenant_id" db:"tenant_id"`
	Classification    string     `json:"classification" db:"classification"`
	OriginalPriority  *string    `json:"original_priority,omitempty" db:"original_priority"`
	CorrectedPriority *string    `json:"corrected_priority,omitempty" db:"corrected_priority"`
	PriorityDirection *string    `json:"priority_direction,omitempty" db:"priority_direction"`
	ReasonCode        string     `json:"reason_code" db:"reason_code"`
	ReasonText        *string    `json:"reason_text,omitempty" db:"reason_text"`
	ApplyScope        string     `json:"apply_scope" db:"apply_scope"`
	ApplyUntil        *time.Time `json:"apply_until,omitempty" db:"apply_until"`
	LinkedEventID     *string    `json:"linked_event_id,omitempty" db:"linked_event_id"`
	ClassifiedBy      string     `json:"classified_by" db:"classified_by"`
	ClassifiedAt      time.Time  `json:"classified_at" db:"classified_at"`
	OriginalScore     *int       `json:"original_score,omitempty" db:"original_score"`
	FeatureSnapshot   *string    `json:"feature_snapshot,omitempty" db:"feature_snapshot"`
	RuleID            *string    `json:"rule_id,omitempty" db:"rule_id"`
}

// TriageRule represents a rule for automatic event processing
type TriageRule struct {
	ID               string  `json:"id" db:"id"`
	TenantID         *string `json:"tenant_id,omitempty" db:"tenant_id"`
	AccountID        *string `json:"account_id,omitempty" db:"account_id"`
	RuleType         string  `json:"rule_type" db:"rule_type"`
	MatchSource      *string `json:"match_source,omitempty" db:"match_source"`
	MatchAlertname   *string `json:"match_alertname,omitempty" db:"match_alertname"`
	MatchNamespace   *string `json:"match_namespace,omitempty" db:"match_namespace"`
	MatchService     *string `json:"match_service,omitempty" db:"match_service"`
	MatchFingerprint *string `json:"match_fingerprint,omitempty" db:"match_fingerprint"`
	MatchLabels      *string `json:"match_labels,omitempty" db:"match_labels"`
	MatchPriority    *string `json:"match_priority,omitempty" db:"match_priority"`
	MatchFindingType *string `json:"match_finding_type,omitempty" db:"match_finding_type"`
	// MatchOccurrenceGreaterThan matches events where occurrence_number > this value
	// Used for system duplicate rule to match 2nd+ occurrences
	MatchOccurrenceGreaterThan *int       `json:"match_occurrence_greater_than,omitempty" db:"match_occurrence_greater_than"`
	Action                     string     `json:"action" db:"action"`
	ActionValue                *string    `json:"action_value,omitempty" db:"action_value"`
	Priority                   int        `json:"priority" db:"priority"`
	IsEditable                 bool       `json:"is_editable" db:"is_editable"`
	CanOverride                bool       `json:"can_override" db:"can_override"`
	OverrideRuleID             *string    `json:"override_rule_id,omitempty" db:"override_rule_id"`
	Enabled                    bool       `json:"enabled" db:"enabled"`
	EffectiveFrom              *time.Time `json:"effective_from,omitempty" db:"effective_from"`
	EffectiveUntil             *time.Time `json:"effective_until,omitempty" db:"effective_until"`
	Name                       *string    `json:"name,omitempty" db:"name"`
	Description                *string    `json:"description,omitempty" db:"description"`
	Reason                     *string    `json:"reason,omitempty" db:"reason"`
	CreatedBy                  *string    `json:"created_by,omitempty" db:"created_by"`
	UpdatedBy                  *string    `json:"updated_by,omitempty" db:"updated_by"`
	CreatedAt                  time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at" db:"updated_at"`
	MatchCount                 int        `json:"match_count" db:"match_count"`
	LastMatchedAt              *time.Time `json:"last_matched_at,omitempty" db:"last_matched_at"`
	ApplyToExisting            bool       `json:"apply_to_existing" db:"apply_to_existing"`
	// Computed fields (not stored in DB, calculated at query time)
	IsSystemRule bool `json:"is_system_rule" db:"-"`
	IsOverridden bool `json:"is_overridden" db:"-"`
}

// TriageRuleMatch records a rule-event match for drilldown tracking.
// All rule types (suppression, scoring, classification) insert into this table
// to provide a single, consistent source of truth for "which events did this rule match".
type TriageRuleMatch struct {
	ID             string    `json:"id" db:"id"`
	EventID        string    `json:"event_id" db:"event_id"`
	RuleID         string    `json:"rule_id" db:"rule_id"`
	CloudAccountID string    `json:"cloud_account_id" db:"cloud_account_id"`
	TenantID       string    `json:"tenant_id" db:"tenant_id"`
	RuleType       string    `json:"rule_type" db:"rule_type"`
	Action         string    `json:"action" db:"action"`
	MatchedAt      time.Time `json:"matched_at" db:"matched_at"`
}

// SystemDefaultDuplicateRuleID is the well-known UUID for the system default auto-duplicate rule
const SystemDefaultDuplicateRuleID = "00000000-0000-0000-0000-000000000001"

// TriageRuleOverride represents an account-level override for a system rule
type TriageRuleOverride struct {
	ID           string    `json:"id" db:"id"`
	SystemRuleID string    `json:"system_rule_id" db:"system_rule_id"`
	TenantID     string    `json:"tenant_id" db:"tenant_id"`
	AccountID    string    `json:"account_id" db:"account_id"`
	Disabled     bool      `json:"disabled" db:"disabled"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

// BulkOperation tracks async bulk classification operations
type BulkOperation struct {
	ID               string     `json:"id" db:"id"`
	OperationType    string     `json:"operation_type" db:"operation_type"`
	Fingerprint      *string    `json:"fingerprint,omitempty" db:"fingerprint"`
	AccountID        string     `json:"account_id" db:"account_id"`
	TargetStatus     string     `json:"target_status" db:"target_status"`
	TotalEvents      int        `json:"total_events" db:"total_events"`
	ProcessedEvents  int        `json:"processed_events" db:"processed_events"`
	Status           string     `json:"status" db:"status"`
	CreatedBy        *string    `json:"created_by,omitempty" db:"created_by"`
	CreatedAt        time.Time  `json:"created_at" db:"created_at"`
	CompletedAt      *time.Time `json:"completed_at,omitempty" db:"completed_at"`
	RuleID           *string    `json:"rule_id,omitempty" db:"rule_id"`
	ClassificationID *string    `json:"classification_id,omitempty" db:"classification_id"`
	ErrorMessage     *string    `json:"error_message,omitempty" db:"error_message"`
}

// Bulk operation status constants
const (
	BulkStatusQueued     = "queued"
	BulkStatusProcessing = "processing"
	BulkStatusCompleted  = "completed"
	BulkStatusFailed     = "failed"
)

// -------------------- Request/Response Types --------------------

// ClassifyPreviewRequest is the request for previewing classification impact
type ClassifyPreviewRequest struct {
	EventID         string `json:"event_id"`
	Classification  string `json:"classification"`
	ApplyScope      string `json:"apply_scope"`
	ApplyUntilHours *int   `json:"apply_until_hours,omitempty"`
}

// ClassifyPreviewResponse shows the impact of classification before confirmation
type ClassifyPreviewResponse struct {
	CurrentEvent   CurrentEventPreview   `json:"current_event"`
	ExistingEvents ExistingEventsPreview `json:"existing_events"`
	FutureEvents   FutureEventsPreview   `json:"future_events"`
	RuleToCreate   *TriageRulePreview    `json:"rule_to_create,omitempty"`
}

// CurrentEventPreview shows what will happen to the current event
type CurrentEventPreview struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	NewStatus string `json:"new_status"`
}

// ExistingEventsPreview shows impact on existing events with same fingerprint
type ExistingEventsPreview struct {
	Count         int      `json:"count"`
	SampleTitles  []string `json:"sample_titles"`
	WillBeUpdated bool     `json:"will_be_updated"`
}

// FutureEventsPreview shows impact on future events
type FutureEventsPreview struct {
	RuleApplies      bool   `json:"rule_applies"`
	ScopeDescription string `json:"scope_description"`
}

// TriageRulePreview shows the rule that will be created
type TriageRulePreview struct {
	RuleType      string     `json:"rule_type"`
	MatchCriteria string     `json:"match_criteria"`
	Action        string     `json:"action"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
}

// ClassifyEventRequest is the full classification request
type ClassifyEventRequest struct {
	EventID           string  `json:"event_id"`
	Classification    string  `json:"classification"`
	ReasonCode        string  `json:"reason_code"`
	ReasonText        *string `json:"reason_text,omitempty"`
	PriorityDirection *string `json:"priority_direction,omitempty"`
	CorrectedPriority *string `json:"corrected_priority,omitempty"`
	ApplyScope        string  `json:"apply_scope"`
	ApplyUntilHours   *int    `json:"apply_until_hours,omitempty"`
	LinkedEventID     *string `json:"linked_event_id,omitempty"`
	ApplyToExisting   bool    `json:"apply_to_existing"`
	Confirmed         bool    `json:"confirmed"`
}

// ClassifyEventResponse is the response after classification
type ClassifyEventResponse struct {
	Success          bool                   `json:"success"`
	ClassificationID string                 `json:"classification_id"`
	RuleCreated      bool                   `json:"rule_created"`
	RuleID           *string                `json:"rule_id,omitempty"`
	RuleExpiresAt    *time.Time             `json:"rule_expires_at,omitempty"`
	BulkOperation    *BulkOperationResponse `json:"bulk_operation,omitempty"`
}

// BulkOperationResponse shows bulk operation status in response
type BulkOperationResponse struct {
	JobID          string `json:"job_id"`
	EventsToUpdate int    `json:"events_to_update"`
	Status         string `json:"status"`
}

// DuplicateSuggestion represents a suggested original event for duplicate classification
type DuplicateSuggestion struct {
	EventID          string    `json:"event_id" db:"id"`
	Title            string    `json:"title" db:"title"`
	StartsAt         time.Time `json:"starts_at" db:"starts_at"`
	OccurrenceNumber int       `json:"occurrence_number" db:"occurrence_number"`
	IsFirst          bool      `json:"is_first" db:"is_first"`
}

// GetDuplicateSuggestionsRequest is the request for duplicate suggestions
type GetDuplicateSuggestionsRequest struct {
	EventID string `json:"event_id"`
}

// GetDuplicateSuggestionsResponse contains the list of suggestions
type GetDuplicateSuggestionsResponse struct {
	Suggestions []DuplicateSuggestion `json:"suggestions"`
}

// -------------------- Triage Rule Result Types --------------------

// TriageRuleResult holds the result of rule evaluation
type TriageRuleResult struct {
	RuleID             string              `json:"rule_id"`
	RuleType           string              `json:"rule_type"`
	Action             string              `json:"action"`
	ActionValue        *ActionValueData    `json:"action_value,omitempty"`
	Suppression        *SuppressionResult  `json:"suppression,omitempty"`
	ScoreAdjustment    *ScoreAdjustment    `json:"score_adjustment,omitempty"`
	AutoClassification *AutoClassification `json:"auto_classification,omitempty"`
}

// SuppressionResult holds suppression rule result
type SuppressionResult struct {
	Action    string `json:"action"` // suppress or drop
	NewStatus string `json:"new_status"`
}

// ScoreAdjustment holds scoring rule result
type ScoreAdjustment struct {
	Adjustment int    `json:"adjustment"`
	Reason     string `json:"reason"`
}

// AutoClassification holds auto-classification rule result
type AutoClassification struct {
	Classification string  `json:"classification"`
	LinkedEventID  *string `json:"linked_event_id,omitempty"`
	ReasonCode     string  `json:"reason_code"`
	NewStatus      string  `json:"new_status"`
	RuleID         *string `json:"rule_id,omitempty"`
}

// ActionValueData represents the parsed action_value JSON
type ActionValueData struct {
	Adjustment     *int    `json:"adjustment,omitempty"`
	Reason         *string `json:"reason,omitempty"`
	LinkedEventID  *string `json:"linked_event_id,omitempty"`
	Classification *string `json:"classification,omitempty"`
	ReasonCode     *string `json:"reason_code,omitempty"`
}

// ParseActionValue parses the action_value JSON string
func ParseActionValue(actionValue *string) (*ActionValueData, error) {
	if actionValue == nil || *actionValue == "" {
		return nil, nil
	}

	var data ActionValueData
	if err := json.Unmarshal([]byte(*actionValue), &data); err != nil {
		return nil, err
	}
	return &data, nil
}

// -------------------- Create Rule Request Types --------------------

// CreateTriageRuleRequest is the request to create a triage rule manually
type CreateTriageRuleRequest struct {
	RuleType         string  `json:"rule_type"`
	MatchSource      *string `json:"match_source,omitempty"`
	MatchAlertname   *string `json:"match_alertname,omitempty"`
	MatchNamespace   *string `json:"match_namespace,omitempty"`
	MatchService     *string `json:"match_service,omitempty"`
	MatchFingerprint *string `json:"match_fingerprint,omitempty"`
	MatchLabels      *string `json:"match_labels,omitempty"`
	MatchPriority    *string `json:"match_priority,omitempty"`
	MatchFindingType *string `json:"match_finding_type,omitempty"`
	Action           string  `json:"action"`
	ActionValue      *string `json:"action_value,omitempty"`
	Priority         *int    `json:"priority,omitempty"`
	EffectiveUntil   *string `json:"effective_until,omitempty"`
	Name             *string `json:"name,omitempty"`
	Description      *string `json:"description,omitempty"`
	ApplyToExisting  bool    `json:"apply_to_existing"`
}

// CreateTriageRuleResponse is the response after creating a rule
type CreateTriageRuleResponse struct {
	Success       bool                   `json:"success"`
	Rule          *TriageRule            `json:"rule,omitempty"`
	Error         *string                `json:"error,omitempty"`
	BulkOperation *BulkOperationResponse `json:"bulk_operation,omitempty"`
}

// RulePreviewRequest is the request to preview how many events match a rule's criteria
type RulePreviewRequest struct {
	RuleType         string  `json:"rule_type"`
	MatchSource      *string `json:"match_source,omitempty"`
	MatchAlertname   *string `json:"match_alertname,omitempty"`
	MatchNamespace   *string `json:"match_namespace,omitempty"`
	MatchService     *string `json:"match_service,omitempty"`
	MatchFingerprint *string `json:"match_fingerprint,omitempty"`
	MatchLabels      *string `json:"match_labels,omitempty"`
	MatchPriority    *string `json:"match_priority,omitempty"`
	MatchFindingType *string `json:"match_finding_type,omitempty"`
	Action           string  `json:"action"`
}

// RulePreviewResponse shows how many existing events would be affected by the rule
type RulePreviewResponse struct {
	MatchingEventsCount int                      `json:"matching_events_count"`
	SampleEvents        []RulePreviewSampleEvent `json:"sample_events"`
	NewStatus           string                   `json:"new_status"`
}

// RulePreviewSampleEvent shows a sample of matching events
type RulePreviewSampleEvent struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Namespace string `json:"namespace,omitempty"`
	Service   string `json:"service,omitempty"`
}

// GetTriageRulesRequest is the request to list triage rules
type GetTriageRulesRequest struct {
	RuleType *string `json:"rule_type,omitempty"`
	Enabled  *bool   `json:"enabled,omitempty"`
}

// GetTriageRulesResponse contains the list of rules
type GetTriageRulesResponse struct {
	Rules []TriageRule `json:"rules"`
}

// DeleteTriageRuleRequest is the request to delete/disable a rule
type DeleteTriageRuleRequest struct {
	RuleID     string `json:"rule_id"`
	HardDelete bool   `json:"hard_delete"`
}

// DeleteTriageRuleResponse is the response after deleting a rule
type DeleteTriageRuleResponse struct {
	Success bool    `json:"success"`
	Error   *string `json:"error,omitempty"`
}

// UpdateTriageRuleRequest is the request to update an existing triage rule
type UpdateTriageRuleRequest struct {
	RuleID           string  `json:"rule_id"`
	RuleType         string  `json:"rule_type"`
	MatchSource      *string `json:"match_source,omitempty"`
	MatchAlertname   *string `json:"match_alertname,omitempty"`
	MatchNamespace   *string `json:"match_namespace,omitempty"`
	MatchService     *string `json:"match_service,omitempty"`
	MatchFingerprint *string `json:"match_fingerprint,omitempty"`
	MatchLabels      *string `json:"match_labels,omitempty"`
	MatchPriority    *string `json:"match_priority,omitempty"`
	MatchFindingType *string `json:"match_finding_type,omitempty"`
	Action           string  `json:"action"`
	ActionValue      *string `json:"action_value,omitempty"`
	Priority         *int    `json:"priority,omitempty"`
	EffectiveUntil   *string `json:"effective_until,omitempty"`
	Name             *string `json:"name,omitempty"`
	Description      *string `json:"description,omitempty"`
	ApplyToExisting  bool    `json:"apply_to_existing"`
}

// UpdateTriageRuleResponse is the response after updating a rule
type UpdateTriageRuleResponse struct {
	Success       bool                   `json:"success"`
	Rule          *TriageRule            `json:"rule,omitempty"`
	Error         *string                `json:"error,omitempty"`
	BulkOperation *BulkOperationResponse `json:"bulk_operation,omitempty"`
}

// GetBulkOperationStatusRequest is the request to check bulk operation status
type GetBulkOperationStatusRequest struct {
	JobID string `json:"job_id"`
}

// GetBulkOperationStatusResponse is the response with bulk operation status
type GetBulkOperationStatusResponse struct {
	JobID           string     `json:"job_id"`
	Status          string     `json:"status"`
	TotalEvents     int        `json:"total_events"`
	ProcessedEvents int        `json:"processed_events"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	ErrorMessage    *string    `json:"error_message,omitempty"`
}

// -------------------- Update NB Status Types --------------------

// UpdateNBStatusRequest is the request to update event nb_status
type UpdateNBStatusRequest struct {
	EventID      string     `json:"event_id"`
	NBStatus     string     `json:"nb_status"`
	SnoozedUntil *time.Time `json:"snoozed_until,omitempty"`
}

// UpdateNBStatusResponse is the response after updating nb_status
type UpdateNBStatusResponse struct {
	Success    bool   `json:"success"`
	PrevStatus string `json:"prev_status"`
	NewStatus  string `json:"new_status"`
}

// -------------------- System Rule Override Types --------------------

// ToggleSystemRuleOverrideRequest is the request to enable/disable a system rule for an account
type ToggleSystemRuleOverrideRequest struct {
	SystemRuleID string `json:"system_rule_id"`
	Disabled     bool   `json:"disabled"`
}

// ToggleSystemRuleOverrideResponse is the response after toggling system rule override
type ToggleSystemRuleOverrideResponse struct {
	Success      bool    `json:"success"`
	Error        *string `json:"error,omitempty"`
	IsOverridden bool    `json:"is_overridden"`
}

// -------------------- Threshold Suggestion Types --------------------

// ThresholdSuggestionRequest is the request for getting threshold suggestions
type ThresholdSuggestionRequest struct {
	EventID string `json:"event_id"`
}

// ThresholdSuggestionResponse contains the threshold analysis and suggestion
type ThresholdSuggestionResponse struct {
	Available       bool                 `json:"available"`
	Source          string               `json:"source"`
	AlertDefinition *AlertDefinition     `json:"alert_definition,omitempty"`
	FiringAnalysis  *FiringAnalysis      `json:"firing_analysis,omitempty"`
	MetricHistory   *MetricHistory       `json:"metric_history,omitempty"`
	Suggestion      *ThresholdSuggestion `json:"suggestion,omitempty"`
	AlertQuality    *AlertQualityScore   `json:"alert_quality,omitempty"`
	QueryMetadata   *MetricQueryMetadata `json:"query_metadata,omitempty"`
	Error           string               `json:"error,omitempty"`
}

// AlertDefinition describes the current alert/alarm configuration
type AlertDefinition struct {
	MetricName        string  `json:"metric_name"`
	MetricNamespace   string  `json:"metric_namespace"`
	Operator          string  `json:"operator"`
	CurrentThreshold  float64 `json:"current_threshold"`
	Aggregation       string  `json:"aggregation"`
	Period            int     `json:"period"`
	EvaluationPeriods int     `json:"evaluation_periods"`
	AlarmName         string  `json:"alarm_name"`
	AlarmARN          string  `json:"alarm_arn,omitempty"`
}

// FiringAnalysis contains statistics about how often the alert fires
type FiringAnalysis struct {
	TotalOccurrences     int       `json:"total_occurrences"`
	TimeRangeDays        int       `json:"time_range_days"`
	AvgFiringsPerDay     float64   `json:"avg_firings_per_day"`
	MetricValuesAtFiring []float64 `json:"metric_values_at_firing"`
}

// MetricHistory contains the actual metric time series from the source API
type MetricHistory struct {
	Timestamps []int64   `json:"timestamps"`
	Values     []float64 `json:"values"`
	StartTime  string    `json:"start_time"`
	EndTime    string    `json:"end_time"`
	Step       int       `json:"step"`
}

// ThresholdSuggestion contains the recommended threshold adjustment
type ThresholdSuggestion struct {
	SuggestedThreshold float64  `json:"suggested_threshold"`
	Reason             string   `json:"reason"`
	Confidence         string   `json:"confidence"`
	MetricP50          float64  `json:"metric_p50"`
	MetricP90          float64  `json:"metric_p90"`
	MetricP95          float64  `json:"metric_p95"`
	MetricP99          float64  `json:"metric_p99"`
	MetricMedian       float64  `json:"metric_median"`
	MetricMAD          float64  `json:"metric_mad"`
	EstimatedReduction float64  `json:"estimated_reduction"`
	Method             string   `json:"method,omitempty"`              // "MAD", "IQR", "P95", "spike"
	RecommendationType string   `json:"recommendation_type,omitempty"` // "tune_threshold", "increase_duration", "tune_both", "disable", "none"
	SuggestedDuration  int      `json:"suggested_duration,omitempty"`  // suggested evaluation window in minutes (0 = no change)
	DurationReason     string   `json:"duration_reason,omitempty"`
	RiskLevel          string   `json:"risk_level,omitempty"`    // "safe", "review", "dangerous" — operational risk of applying this suggestion
	RiskWarnings       []string `json:"risk_warnings,omitempty"` // human-readable warnings about why this suggestion needs careful review
}

// MetricQueryMetadata stores the parameters needed to re-query the same metric from the frontend.
type MetricQueryMetadata struct {
	MetricProvider  string              `json:"metric_provider"`
	ServiceName     string              `json:"service_name,omitempty"`
	Region          string              `json:"region,omitempty"`
	MetricNames     []string            `json:"metric_names,omitempty"`
	MetricNamespace string              `json:"metric_namespace,omitempty"`
	Dimensions      []map[string]string `json:"dimensions,omitempty"`
	Statistics      []string            `json:"statistics,omitempty"`
	ResourceIds     []string            `json:"resource_ids,omitempty"`
	PromQL          string              `json:"promql,omitempty"`
}

// AlertQualityScore classifies alert health based on firing patterns
type AlertQualityScore struct {
	FlappingRate    float64 `json:"flapping_rate"`      // events with duration < 5min / total
	MeanTimeToClose float64 `json:"mean_time_to_close"` // avg seconds to resolve
	FiringFrequency float64 `json:"firing_frequency"`   // events per day
	ResolutionRate  float64 `json:"resolution_rate"`    // % of events that resolve (excludes instant events)
	EngagementRate  float64 `json:"engagement_rate"`    // % triaged by a human
	TransientRate   float64 `json:"transient_rate"`     // events resolving < 10 min / total resolved
	DurationP90     float64 `json:"duration_p90"`       // 90th percentile of event duration in seconds
	InstantRate     float64 `json:"instant_rate"`       // events where ends_at = starts_at / total
	Classification  string  `json:"classification"`     // false_positive|noisy_but_real|broken|healthy
	Recommendation  string  `json:"recommendation"`     // tune_threshold|increase_duration|disable_alert|investigate|no_action
}
