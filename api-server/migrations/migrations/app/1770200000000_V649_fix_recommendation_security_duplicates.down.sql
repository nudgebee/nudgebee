-- Rollback: Remove NULLS NOT DISTINCT from the constraint
-- Note: This does NOT un-archive the previously archived duplicates

ALTER TABLE recommendation DROP CONSTRAINT IF EXISTS recommendation_cloud_account_id_rule_name_resource_id_category_;

ALTER TABLE recommendation ADD CONSTRAINT recommendation_cloud_account_id_rule_name_resource_id_category_
    UNIQUE (cloud_account_id, rule_name, resource_id, category, account_object_id);
