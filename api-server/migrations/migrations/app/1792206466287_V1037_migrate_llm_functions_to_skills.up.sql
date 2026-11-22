-- Saved AI functions become user-defined skills (#41512). Each function is
-- copied into its account as a text skill with the same name, so "/name" in
-- chat keeps working: it now loads the skill. llm_functions is left in place
-- for pods still running the previous release, and dropped later.
--
-- Skipped, as the knowledge-base form would refuse them: a function with no
-- account, one whose account is not in its tenant, a name the form would not
-- accept, and a name the account already uses for a knowledge base (any case).
-- Of two functions whose names differ only in case, the newest is kept.
--
-- The copies are not indexed for search, so knowledge search finds them only
-- once they are saved again; "/name" and loading a skill by name read the row.
-- Small table (tens of rows); no lock concern.
INSERT INTO llm_knowledgebases
    (tenant_id, account_id, name, description, data, data_format, data_filename, data_size_bytes,
     status, kb_type, visibility, enabled, created_by, updated_by, created_at, updated_at)
SELECT f.tenant_id, f.account_id, BTRIM(f.name), NULLIF(BTRIM(f.description), ''), f.data, 'text',
       BTRIM(f.name) || '.txt', octet_length(f.data), 'active', 'manual', 'account', true,
       f.created_by, f.updated_by, f.created_at, NOW()
FROM (
    SELECT DISTINCT ON (fn.account_id, LOWER(BTRIM(fn.name)))
           fn.*,
           fn.prompt || CASE
               WHEN fn.variables IS NULL OR fn.variables IN ('[]'::jsonb, '{}'::jsonb, 'null'::jsonb) THEN ''
               ELSE E'\n\nInputs: ' || fn.variables::text
           END AS data
    FROM llm_functions fn
    WHERE fn.account_id IS NOT NULL
      AND BTRIM(fn.name) ~ '^[a-zA-Z][a-zA-Z0-9_ \-:]{2,99}$'
    ORDER BY fn.account_id, LOWER(BTRIM(fn.name)), fn.updated_at DESC
) f
JOIN cloud_accounts ca ON ca.id = f.account_id AND ca.tenant = f.tenant_id
WHERE NOT EXISTS (
    SELECT 1 FROM llm_knowledgebases kb
    WHERE kb.account_id = f.account_id
      AND LOWER(BTRIM(kb.name)) = LOWER(BTRIM(f.name))
);

-- The "Custom AI functions" feature switch gated only the Functions screens,
-- which are gone; remove it so Tenant Settings stops listing a dead toggle.
DELETE FROM feature_flag WHERE feature_id = 'LLM_FUNCTION';
DELETE FROM billing_feature_mapping WHERE feature_id = 'LLM_FUNCTION';
DELETE FROM billing_plan_features WHERE feature_id = 'LLM_FUNCTION';
DELETE FROM feature WHERE value = 'LLM_FUNCTION';
