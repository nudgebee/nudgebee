DELETE FROM public.recommendation_rule
WHERE rule_name IN (
  'aws_native_purchase_reserved_instances',
  'aws_native_purchase_savings_plans',
  'aws_native_rightsize',
  'aws_native_stop',
  'aws_native_delete',
  'aws_native_upgrade',
  'aws_native_migrate_graviton'
);
