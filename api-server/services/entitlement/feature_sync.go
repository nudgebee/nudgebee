package entitlement

import (
	"context"
	"log/slog"
	"nudgebee/services/internal/database"
)

// SyncTenantFeatures enables features for a tenant based on their plan
// This is additive - it only enables features, never disables existing ones
func (s *Service) SyncTenantFeatures(ctx context.Context, tenantID, planID string) error {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	// Get features included in the plan
	rows, err := dbm.Db.QueryContext(ctx, `
		SELECT feature_id FROM billing_plan_features WHERE plan_id = $1
	`, planID)
	if err != nil {
		slog.Error("Failed to get plan features", "planID", planID, "error", err)
		return err
	}
	defer func() { _ = rows.Close() }()

	var features []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return err
		}
		features = append(features, f)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Enable each feature for the tenant (additive approach)
	for _, feature := range features {
		_, err := dbm.Db.ExecContext(ctx, `
			INSERT INTO feature_flag (tenant_id, feature_id, status)
			VALUES ($1, $2, 'enabled')
			ON CONFLICT (tenant_id, feature_id) DO UPDATE SET status = 'enabled'
		`, tenantID, feature)
		if err != nil {
			slog.Error("Failed to enable feature for tenant", "tenantID", tenantID, "feature", feature, "error", err)
			// Continue with other features even if one fails
		}
	}

	slog.Info("Synced tenant features from plan", "tenantID", tenantID, "planID", planID, "features", features)
	return nil
}

// GetPlanFeatures returns all features included in a plan
func (s *Service) GetPlanFeatures(ctx context.Context, planID string) ([]string, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	rows, err := dbm.Db.QueryContext(ctx, `
		SELECT feature_id FROM billing_plan_features WHERE plan_id = $1
	`, planID)
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

	return features, nil
}

// CreateSubscription creates a new subscription for a tenant and syncs features
func (s *Service) CreateSubscription(ctx context.Context, tenantID, planID string, marketplaceCustomerID *string) (*Subscription, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	var sub Subscription
	err = dbm.Db.QueryRowContext(ctx, `
		INSERT INTO billing_subscriptions (tenant_id, plan_id, marketplace_customer_id, billing_cycle_start)
		VALUES ($1, $2, $3, date_trunc('month', now()))
		ON CONFLICT (tenant_id) DO UPDATE SET
			plan_id = EXCLUDED.plan_id,
			marketplace_customer_id = COALESCE(EXCLUDED.marketplace_customer_id, billing_subscriptions.marketplace_customer_id),
			updated_at = now()
		RETURNING id, tenant_id, plan_id, status, subscription_start, created_at
	`, tenantID, planID, marketplaceCustomerID).Scan(
		&sub.ID, &sub.TenantID, &sub.PlanID, &sub.Status, &sub.SubscriptionStart, &sub.CreatedAt)

	if err != nil {
		slog.Error("Failed to create subscription", "tenantID", tenantID, "planID", planID, "error", err)
		return nil, err
	}

	// Sync features for the new subscription
	if err := s.SyncTenantFeatures(ctx, tenantID, planID); err != nil {
		slog.Error("Failed to sync features after subscription", "tenantID", tenantID, "planID", planID, "error", err)
		// Don't fail the subscription creation, just log the error
	}

	slog.Info("Created subscription", "tenantID", tenantID, "planID", planID, "subscriptionID", sub.ID)
	return &sub, nil
}

// UpdateSubscriptionPlan updates a tenant's subscription to a new plan
func (s *Service) UpdateSubscriptionPlan(ctx context.Context, tenantID, newPlanID string) error {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	_, err = dbm.Db.ExecContext(ctx, `
		UPDATE billing_subscriptions
		SET plan_id = $2, updated_at = now()
		WHERE tenant_id = $1
	`, tenantID, newPlanID)

	if err != nil {
		slog.Error("Failed to update subscription plan", "tenantID", tenantID, "newPlanID", newPlanID, "error", err)
		return err
	}

	// Sync features for the new plan
	if err := s.SyncTenantFeatures(ctx, tenantID, newPlanID); err != nil {
		slog.Error("Failed to sync features after plan update", "tenantID", tenantID, "newPlanID", newPlanID, "error", err)
	}

	slog.Info("Updated subscription plan", "tenantID", tenantID, "newPlanID", newPlanID)
	return nil
}

// GetTenantSubscription returns the current subscription for a tenant
func (s *Service) GetTenantSubscription(ctx context.Context, tenantID string) (*Subscription, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}

	var sub Subscription
	err = dbm.Db.QueryRowContext(ctx, `
		SELECT id, tenant_id, plan_id, status, subscription_start, subscription_end,
		       entitlement_overrides, marketplace_customer_id, billing_cycle_start,
		       is_private_contract, created_at, updated_at
		FROM billing_subscriptions WHERE tenant_id = $1
	`, tenantID).Scan(
		&sub.ID, &sub.TenantID, &sub.PlanID, &sub.Status, &sub.SubscriptionStart,
		&sub.SubscriptionEnd, &sub.EntitlementOverrides, &sub.MarketplaceCustomerID,
		&sub.BillingCycleStart, &sub.IsPrivateContract, &sub.CreatedAt, &sub.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return &sub, nil
}

// SetEntitlementOverrides sets custom entitlement overrides for a tenant
func (s *Service) SetEntitlementOverrides(ctx context.Context, tenantID string, overrides EntitlementOverrides) error {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return err
	}

	_, err = dbm.Db.ExecContext(ctx, `
		UPDATE billing_subscriptions
		SET entitlement_overrides = $2, is_private_contract = true, updated_at = now()
		WHERE tenant_id = $1
	`, tenantID, overrides)

	if err != nil {
		slog.Error("Failed to set entitlement overrides", "tenantID", tenantID, "error", err)
		return err
	}

	slog.Info("Set entitlement overrides", "tenantID", tenantID)
	return nil
}
