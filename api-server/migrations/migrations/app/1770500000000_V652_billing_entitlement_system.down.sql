-- V650: Rollback Billing Entitlement System

-- Drop tables in reverse order (respecting foreign key dependencies)
DROP TABLE IF EXISTS billing_usage_events;
DROP TABLE IF EXISTS billing_usage;
DROP TABLE IF EXISTS billing_subscriptions;
DROP TABLE IF EXISTS billing_plan_dimension_limits;
DROP TABLE IF EXISTS billing_plan_features;
DROP TABLE IF EXISTS billing_plans;
DROP TABLE IF EXISTS billing_feature_mapping;

-- Remove inserted features (optional - keep if features might be used elsewhere)
-- DELETE FROM feature WHERE value IN ('TROUBLESHOOT', 'OPTIMIZE');
