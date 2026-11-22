-- Drops the LLM_DECISION_MODELS feature registered in V1016 (#40902). It only
-- gated saving a decision model in a tenant's LLM settings, not decision-model
-- use or server settings, so it is removed: whoever configures the LLM decides.
-- Every table that references feature(value) is cleared first, as in V889.
-- Small tables, single-row deletes. Idempotent.

DELETE FROM feature_flag WHERE feature_id = 'LLM_DECISION_MODELS';
DELETE FROM billing_feature_mapping WHERE feature_id = 'LLM_DECISION_MODELS';
DELETE FROM billing_plan_features WHERE feature_id = 'LLM_DECISION_MODELS';
DELETE FROM feature WHERE value = 'LLM_DECISION_MODELS';
