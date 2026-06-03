-- Backfill dedupe_group on existing AWS commitment recommendations so the new
-- aggregator picks the highest-savings primary for each (account, service)
-- bucket instead of summing every Savings Plan / Reserved Instance variant
-- AWS Cost Explorer / Cost Optimization Hub returns for the same workload.
--
-- After this point, fresh ingestions populate dedupe_group at write time
-- (see collector-server/cloud-collector/providers/aws/aws_cost_explorer.go
-- and aws_cost_optimization_hub.go); this migration only catches rows that
-- existed before the producer change shipped.
UPDATE recommendation r SET dedupe_group =
    'aws_commitment:' || ca.account_number || ':' || (
        CASE r.rule_name
            WHEN 'aws_native_ce_savings_plan_recommendation' THEN
                CASE r.recommendation->>'savings_plan_type'
                    WHEN 'EC2_INSTANCE_SP' THEN 'AmazonEC2'
                    WHEN 'COMPUTE_SP'      THEN 'AmazonEC2'
                    WHEN 'SAGEMAKER_SP'    THEN 'AmazonSageMaker'
                END
            WHEN 'aws_native_database_savings_plan' THEN 'AmazonRDS'
            WHEN 'aws_native_ce_ri_recommendation' THEN
                CASE r.recommendation->>'service'
                    WHEN 'Amazon Elastic Compute Cloud - Compute' THEN 'AmazonEC2'
                    WHEN 'Amazon Relational Database Service'     THEN 'AmazonRDS'
                    WHEN 'Amazon OpenSearch Service'              THEN 'AmazonOpenSearchService'
                    WHEN 'Amazon ElastiCache'                     THEN 'AmazonElastiCache'
                    WHEN 'Amazon Redshift'                        THEN 'AmazonRedshift'
                    WHEN 'Amazon MemoryDB Service'                THEN 'AmazonMemoryDB'
                    WHEN 'Amazon DynamoDB Service'                THEN 'AmazonDynamoDB'
                END
            WHEN 'aws_native_purchase_savings_plans' THEN
                CASE r.recommendation->>'current_resource_type'
                    WHEN 'ComputeSavingsPlans'     THEN 'AmazonEC2'
                    WHEN 'Ec2InstanceSavingsPlans' THEN 'AmazonEC2'
                    WHEN 'SageMakerSavingsPlans'   THEN 'AmazonSageMaker'
                END
            WHEN 'aws_native_purchase_reserved_instances' THEN
                CASE r.recommendation->>'current_resource_type'
                    WHEN 'Ec2ReservedInstances'          THEN 'AmazonEC2'
                    WHEN 'RdsReservedInstances'          THEN 'AmazonRDS'
                    WHEN 'OpenSearchReservedInstances'   THEN 'AmazonOpenSearchService'
                    WHEN 'ElastiCacheReservedInstances'  THEN 'AmazonElastiCache'
                    WHEN 'RedshiftReservedInstances'     THEN 'AmazonRedshift'
                END
        END
    )
FROM cloud_accounts ca
WHERE ca.id = r.cloud_account_id
  AND r.dedupe_group IS NULL
  AND r.rule_name IN (
        'aws_native_ce_savings_plan_recommendation',
        'aws_native_ce_ri_recommendation',
        'aws_native_purchase_savings_plans',
        'aws_native_purchase_reserved_instances',
        'aws_native_database_savings_plan'
  );
