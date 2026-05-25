-- Reverse: clear backfilled dedupe_group values. The column itself is dropped
-- by the V714 down migration; this only resets values populated by V715.
UPDATE recommendation
SET dedupe_group = NULL
WHERE dedupe_group LIKE 'aws_commitment:%'
  AND rule_name IN (
        'aws_native_ce_savings_plan_recommendation',
        'aws_native_ce_ri_recommendation',
        'aws_native_purchase_savings_plans',
        'aws_native_purchase_reserved_instances',
        'aws_native_database_savings_plan'
  );
