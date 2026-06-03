-- Fix duplicate recommendations caused by NULL resource_id not being deduplicated
-- The unique constraint was not using NULLS NOT DISTINCT, so duplicate rows could be inserted
-- when resource_id is NULL (e.g. pv_rightsize, k8s_api_deprecated, certificate_expiry)

-- Step 1: Remove recommendation_resolution rows that reference duplicate recommendations
-- about to be deleted (318 rows). The FK is RESTRICT so we must clean these up first.
DELETE FROM recommendation_resolution
WHERE recommendation_id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY cloud_account_id, rule_name, resource_id, category, account_object_id
                   ORDER BY
                       CASE status WHEN 'Open' THEN 0 WHEN 'Closed' THEN 1 ELSE 2 END,
                       created_at DESC,
                       id DESC
               ) as row_num
        FROM recommendation
        WHERE resource_id IS NULL
    ) duplicates
    WHERE row_num > 1
);

-- Step 2: Delete duplicate rows across ALL statuses, keeping one row per unique key.
-- Keeps the row with the latest non-Archive status (preferring Open > Closed > Archive),
-- breaking ties by most recent created_at, then largest id.
DELETE FROM recommendation
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY cloud_account_id, rule_name, resource_id, category, account_object_id
                   ORDER BY
                       CASE status WHEN 'Open' THEN 0 WHEN 'Closed' THEN 1 ELSE 2 END,
                       created_at DESC,
                       id DESC
               ) as row_num
        FROM recommendation
        WHERE resource_id IS NULL
    ) duplicates
    WHERE row_num > 1
);

-- Step 3: Drop the old constraint
ALTER TABLE recommendation DROP CONSTRAINT IF EXISTS recommendation_cloud_account_id_rule_name_resource_id_category_;

-- Step 4: Recreate the constraint with NULLS NOT DISTINCT
-- This ensures NULL resource_id values are treated as equal for uniqueness
ALTER TABLE recommendation ADD CONSTRAINT recommendation_cloud_account_id_rule_name_resource_id_category_
    UNIQUE NULLS NOT DISTINCT (cloud_account_id, rule_name, resource_id, category, account_object_id);
