package entitlement

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"nudgebee/services/internal/database"
	"nudgebee/services/tenant"
	"sync"
	"time"
)

// Service provides entitlement checking and usage tracking functionality
type Service struct {
	cache *Cache
}

var (
	defaultService *Service
	serviceOnce    sync.Once
)

// GetService returns the singleton entitlement service instance
func GetService() *Service {
	serviceOnce.Do(func() {
		defaultService = &Service{
			cache: NewCache(),
		}
	})
	return defaultService
}

// CheckEntitlement verifies if a tenant can use a feature/dimension
func (s *Service) CheckEntitlement(ctx context.Context, tenantID, dimension string) (*EntitlementStatus, error) {
	// 1. Check bypass first (dev/test tenants)
	if s.isBypassEnabled(ctx, tenantID) {
		return &EntitlementStatus{
			Allowed:   true,
			Remaining: -1,
			Limit:     -1,
			Message:   "Entitlement bypass enabled",
		}, nil
	}

	// 2. Get feature mapping for this dimension
	mapping, err := s.getFeatureMapping(ctx, dimension)
	if err != nil {
		slog.Error("Failed to get feature mapping", "dimension", dimension, "error", err)
		return nil, fmt.Errorf("unknown dimension: %s", dimension)
	}

	// 3. Check if feature is enabled for tenant
	if !tenant.IsFeatureEnabled(nil, tenantID, mapping.FeatureID) {
		return &EntitlementStatus{
			Allowed: false,
			Message: fmt.Sprintf("Feature %s not included in your plan", mapping.FeatureID),
		}, nil
	}

	// 4. For non-metered features (like OPTIMIZE), just check feature flag
	if mapping.AWSMeteredDimension == nil || *mapping.AWSMeteredDimension == "" {
		return &EntitlementStatus{
			Allowed:   true,
			Remaining: -1,
			Limit:     -1,
		}, nil
	}

	// 5. Get effective limit for this tenant/dimension
	limit, err := s.getEffectiveLimit(ctx, tenantID, dimension)
	if err != nil {
		slog.Error("Failed to get effective limit", "tenantID", tenantID, "dimension", dimension, "error", err)
		return nil, err
	}

	// Unlimited (-1) means always allow
	if limit == -1 {
		return &EntitlementStatus{
			Allowed:   true,
			Remaining: -1,
			Limit:     -1,
		}, nil
	}

	// 6. Get current usage for billing period
	billingPeriod := getFirstDayOfMonth(time.Now())
	usage, err := s.getCurrentUsage(ctx, tenantID, dimension, billingPeriod)
	if err != nil {
		slog.Error("Failed to get current usage", "tenantID", tenantID, "dimension", dimension, "error", err)
		return nil, err
	}

	// 7. Compare usage vs limit
	if usage >= limit {
		overageEnabled := s.isOverageEnabled(ctx, tenantID)
		if overageEnabled {
			return &EntitlementStatus{
				Allowed:        true,
				Used:           usage,
				Limit:          limit,
				Remaining:      0,
				OverageEnabled: true,
				OverageCount:   usage - limit,
			}, nil
		}
		// At limit, no overage: graceful degradation
		return &EntitlementStatus{
			Allowed:         false,
			GracefulDegrade: true,
			Used:            usage,
			Limit:           limit,
			Remaining:       0,
			Message:         fmt.Sprintf("%s limit reached for this billing period", dimension),
		}, nil
	}

	// Under limit: allow
	return &EntitlementStatus{
		Allowed:   true,
		Used:      usage,
		Limit:     limit,
		Remaining: limit - usage,
	}, nil
}

// RecordUsage records a usage event and updates aggregated usage
func (s *Service) RecordUsage(ctx context.Context, req RecordUsageRequest) (*RecordUsageResponse, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	billingPeriod := getFirstDayOfMonth(time.Now())
	isBillable := true
	if req.IsBillable != nil {
		isBillable = *req.IsBillable
	}

	// 1. Insert usage event
	_, err = dbm.Db.ExecContext(ctx, `
		INSERT INTO billing_usage_events (tenant_id, dimension, reference_id, reference_type, session_id, is_billable)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, req.TenantID, req.Dimension, req.ReferenceID, req.ReferenceType, req.SessionID, isBillable)
	if err != nil {
		slog.Error("Failed to insert usage event", "error", err)
		return nil, err
	}

	// 2. If not billable (e.g., follow-up), don't increment usage count
	if !isBillable {
		return &RecordUsageResponse{Recorded: true}, nil
	}

	// 3. Get effective limit
	limit, err := s.getEffectiveLimit(ctx, req.TenantID, req.Dimension)
	if err != nil {
		limit = 0 // Default to 0 if we can't get limit
	}

	// 4. Upsert aggregated usage
	var newUsageCount, newOverageCount int
	err = dbm.Db.QueryRowContext(ctx, `
		INSERT INTO billing_usage (tenant_id, billing_period, dimension, usage_count, included_limit, overage_count, updated_at)
		VALUES ($1, $2, $3, 1, $4, CASE WHEN 1 > $4 THEN 1 ELSE 0 END, now())
		ON CONFLICT (tenant_id, billing_period, dimension) DO UPDATE SET
			usage_count = billing_usage.usage_count + 1,
			overage_count = CASE
				WHEN billing_usage.usage_count + 1 > billing_usage.included_limit
				THEN billing_usage.overage_count + 1
				ELSE billing_usage.overage_count
			END,
			updated_at = now()
		RETURNING usage_count, overage_count
	`, req.TenantID, billingPeriod, req.Dimension, limit).Scan(&newUsageCount, &newOverageCount)

	if err != nil {
		slog.Error("Failed to upsert usage", "error", err)
		return nil, err
	}

	// 5. Invalidate cache
	s.cache.InvalidateUsage(req.TenantID, req.Dimension, billingPeriod)

	return &RecordUsageResponse{
		Recorded:        true,
		IsOverage:       newOverageCount > 0,
		NewUsageCount:   newUsageCount,
		NewOverageCount: newOverageCount,
	}, nil
}

// IsNewIncident checks if this is a new billable incident or a follow-up
func (s *Service) IsNewIncident(ctx context.Context, tenantID, eventID, sessionID string) (bool, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false, err
	}

	// Check if session already has a billable incident
	var count int
	err = dbm.Db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM billing_usage_events
		WHERE tenant_id = $1 AND session_id = $2 AND dimension = $3 AND is_billable = true
	`, tenantID, sessionID, DimensionIncidents).Scan(&count)

	if err != nil {
		return false, err
	}

	// If count > 0, this is a follow-up (not new)
	return count == 0, nil
}

// GetTenantStatus returns the full entitlement status for a tenant
func (s *Service) GetTenantStatus(ctx context.Context, tenantID string) (*GetStatusResponse, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	// Get subscription
	var sub Subscription
	var plan Plan
	err = dbm.Db.QueryRowContext(ctx, `
		SELECT s.id, s.tenant_id, s.plan_id, s.status, s.subscription_start,
		       p.name, p.display_name, p.plan_type
		FROM billing_subscriptions s
		JOIN billing_plans p ON s.plan_id = p.id
		WHERE s.tenant_id = $1
	`, tenantID).Scan(&sub.ID, &sub.TenantID, &sub.PlanID, &sub.Status, &sub.SubscriptionStart,
		&plan.Name, &plan.DisplayName, &plan.PlanType)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &GetStatusResponse{
				TenantID: tenantID,
				Status:   "no_subscription",
			}, nil
		}
		return nil, err
	}

	billingPeriod := getFirstDayOfMonth(time.Now())

	// Get features for this plan
	rows, err := dbm.Db.QueryContext(ctx, `
		SELECT feature_id FROM billing_plan_features WHERE plan_id = $1
	`, sub.PlanID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var features []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		features = append(features, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Get dimension status
	var dimensions []DimensionStatus
	mappings, err := s.getAllFeatureMappings(ctx)
	if err != nil {
		return nil, err
	}

	for _, mapping := range mappings {
		limit, _ := s.getEffectiveLimit(ctx, tenantID, mapping.Dimension)
		usage, _ := s.getCurrentUsage(ctx, tenantID, mapping.Dimension, billingPeriod)

		remaining := limit - usage
		if limit == -1 {
			remaining = -1
		}

		overage := 0
		if usage > limit && limit != -1 {
			overage = usage - limit
		}

		dimensions = append(dimensions, DimensionStatus{
			Dimension:      mapping.Dimension,
			Feature:        mapping.FeatureID,
			Used:           usage,
			Limit:          limit,
			Remaining:      remaining,
			OverageCount:   overage,
			OverageEnabled: s.isOverageEnabled(ctx, tenantID),
		})
	}

	return &GetStatusResponse{
		TenantID:      tenantID,
		PlanName:      plan.DisplayName,
		PlanType:      plan.PlanType,
		Status:        string(sub.Status),
		BillingPeriod: billingPeriod.Format("2006-01"),
		Dimensions:    dimensions,
		Features:      features,
	}, nil
}

// GetOverageForBilling returns new overage counts for AWS metering
func (s *Service) GetOverageForBilling(ctx context.Context, tenantID string, billingPeriod time.Time) ([]OverageReport, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	rows, err := dbm.Db.QueryContext(ctx, `
		SELECT u.dimension, m.aws_metered_dimension, u.overage_count, u.last_reported_overage
		FROM billing_usage u
		JOIN billing_feature_mapping m ON u.dimension = m.dimension
		WHERE u.tenant_id = $1 AND u.billing_period = $2
		  AND m.aws_metered_dimension IS NOT NULL
		  AND u.overage_count > u.last_reported_overage
	`, tenantID, billingPeriod)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var reports []OverageReport
	for rows.Next() {
		var r OverageReport
		r.TenantID = tenantID
		if err := rows.Scan(&r.Dimension, &r.AWSMeteredDimension, &r.TotalOverage, &r.LastReportedOverage); err != nil {
			return nil, err
		}
		r.NewOverage = r.TotalOverage - r.LastReportedOverage
		reports = append(reports, r)
	}

	return reports, nil
}

// MarkOverageAsReported updates the last_reported_overage after AWS metering
func (s *Service) MarkOverageAsReported(ctx context.Context, tenantID, dimension string, billingPeriod time.Time, reportedCount int) error {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	_, err = dbm.Db.ExecContext(ctx, `
		UPDATE billing_usage
		SET last_reported_overage = last_reported_overage + $4, updated_at = now()
		WHERE tenant_id = $1 AND billing_period = $2 AND dimension = $3
	`, tenantID, billingPeriod, dimension, reportedCount)

	return err
}

// Helper functions

func (s *Service) isBypassEnabled(ctx context.Context, tenantID string) bool {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false
	}

	var value string
	err = dbm.Db.QueryRowContext(ctx, `
		SELECT value FROM tenant_attrs WHERE tenant_id = $1 AND name = 'entitlement_bypass'
	`, tenantID).Scan(&value)

	if err != nil {
		return false
	}

	return value == "true"
}

func (s *Service) getFeatureMapping(ctx context.Context, dimension string) (*FeatureMapping, error) {
	// Try cache first
	if mapping := s.cache.GetFeatureMapping(dimension); mapping != nil {
		return mapping, nil
	}

	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	var mapping FeatureMapping
	err = dbm.Db.QueryRowContext(ctx, `
		SELECT id, feature_id, dimension, aws_metered_dimension, overage_rate, included_limit_default, description
		FROM billing_feature_mapping WHERE dimension = $1
	`, dimension).Scan(&mapping.ID, &mapping.FeatureID, &mapping.Dimension, &mapping.AWSMeteredDimension,
		&mapping.OverageRate, &mapping.IncludedLimitDefault, &mapping.Description)

	if err != nil {
		return nil, err
	}

	s.cache.SetFeatureMapping(dimension, &mapping)
	return &mapping, nil
}

func (s *Service) getAllFeatureMappings(ctx context.Context) ([]FeatureMapping, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	rows, err := dbm.Db.QueryContext(ctx, `
		SELECT id, feature_id, dimension, aws_metered_dimension, overage_rate, included_limit_default, description
		FROM billing_feature_mapping
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var mappings []FeatureMapping
	for rows.Next() {
		var m FeatureMapping
		if err := rows.Scan(&m.ID, &m.FeatureID, &m.Dimension, &m.AWSMeteredDimension,
			&m.OverageRate, &m.IncludedLimitDefault, &m.Description); err != nil {
			return nil, err
		}
		mappings = append(mappings, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return mappings, nil
}

func (s *Service) getEffectiveLimit(ctx context.Context, tenantID, dimension string) (int, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return 0, err
	}

	// 1. Check tenant-level overrides first
	var overridesJSON sql.NullString
	err = dbm.Db.QueryRowContext(ctx, `
		SELECT entitlement_overrides FROM billing_subscriptions WHERE tenant_id = $1
	`, tenantID).Scan(&overridesJSON)

	if err == nil && overridesJSON.Valid && overridesJSON.String != "" {
		var overrides EntitlementOverrides
		if unmarshalErr := json.Unmarshal([]byte(overridesJSON.String), &overrides); unmarshalErr != nil {
			slog.Warn("Failed to unmarshal entitlement_overrides", "tenantID", tenantID, "error", unmarshalErr)
		} else {
			switch dimension {
			case DimensionIncidents:
				if overrides.Incidents != nil {
					return *overrides.Incidents, nil
				}
			case DimensionWorkflowExecutions:
				if overrides.WorkflowExecutions != nil {
					return *overrides.WorkflowExecutions, nil
				}
			case DimensionAIWorkflowSteps:
				if overrides.AIWorkflowSteps != nil {
					return *overrides.AIWorkflowSteps, nil
				}
			}
		}
	}

	// 2. Check plan-specific dimension limits
	var planLimit sql.NullInt64
	err = dbm.Db.QueryRowContext(ctx, `
		SELECT pdl.included_limit
		FROM billing_plan_dimension_limits pdl
		JOIN billing_subscriptions s ON pdl.plan_id = s.plan_id
		WHERE s.tenant_id = $1 AND pdl.dimension = $2
	`, tenantID, dimension).Scan(&planLimit)

	if err == nil && planLimit.Valid {
		return int(planLimit.Int64), nil
	}

	// 3. Fall back to default from feature mapping
	var defaultLimit sql.NullInt64
	err = dbm.Db.QueryRowContext(ctx, `
		SELECT included_limit_default FROM billing_feature_mapping WHERE dimension = $1
	`, dimension).Scan(&defaultLimit)

	if err == nil && defaultLimit.Valid {
		return int(defaultLimit.Int64), nil
	}

	// No limit found - return unlimited
	return -1, nil
}

func (s *Service) getCurrentUsage(ctx context.Context, tenantID, dimension string, billingPeriod time.Time) (int, error) {
	// Try cache first
	if usage := s.cache.GetUsage(tenantID, dimension, billingPeriod); usage >= 0 {
		return usage, nil
	}

	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return 0, err
	}

	var usageCount int
	err = dbm.Db.QueryRowContext(ctx, `
		SELECT COALESCE(usage_count, 0) FROM billing_usage
		WHERE tenant_id = $1 AND billing_period = $2 AND dimension = $3
	`, tenantID, billingPeriod, dimension).Scan(&usageCount)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}

	s.cache.SetUsage(tenantID, dimension, billingPeriod, usageCount)
	return usageCount, nil
}

func (s *Service) isOverageEnabled(ctx context.Context, tenantID string) bool {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return false
	}

	// Only enable overage if explicitly set via entitlement_overrides
	var overridesJSON sql.NullString
	err = dbm.Db.QueryRowContext(ctx, `
		SELECT entitlement_overrides FROM billing_subscriptions WHERE tenant_id = $1
	`, tenantID).Scan(&overridesJSON)

	if err == nil && overridesJSON.Valid && overridesJSON.String != "" {
		var overrides EntitlementOverrides
		if unmarshalErr := json.Unmarshal([]byte(overridesJSON.String), &overrides); unmarshalErr != nil {
			slog.Warn("Failed to unmarshal entitlement_overrides for overage check", "tenantID", tenantID, "error", unmarshalErr)
		} else if overrides.OverageEnabled != nil {
			return *overrides.OverageEnabled
		}
	}

	// Default: overage/metering disabled (requires explicit opt-in)
	return false
}

func getFirstDayOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
}
