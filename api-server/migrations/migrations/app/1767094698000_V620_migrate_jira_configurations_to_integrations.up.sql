-- Migrate data from jira_configurations to integrations table
-- Step 1: Insert into integrations table
INSERT INTO integrations (id, tenant_id, type, source, name, status, created_at, updated_at, created_by, updated_by, labels)
SELECT
  jc.id,
  jc.tenant,
  CASE jc.tool
    WHEN 'github' THEN 'github_issues'
    ELSE jc.tool
  END,
  'user',
  jc.name,
  CASE
    WHEN jc.is_active = true THEN 'enabled'
    ELSE 'disabled'
  END,
  jc.created_at,
  jc.updated_at,
  jc.created_by,
  jc.updated_by,
  '{}'::json
FROM jira_configurations jc
ON CONFLICT (id) DO NOTHING;

-- Step 2: Insert config values for url
INSERT INTO integration_config_values (id, integration_id, name, value, type, is_encrypted, created_at, updated_at, created_by, updated_by)
SELECT
  gen_random_uuid(),
  jc.id,
  'url',
  jc.url,
  'string',
  false,
  jc.created_at,
  jc.updated_at,
  jc.created_by,
  jc.updated_by
FROM jira_configurations jc
WHERE jc.url IS NOT NULL
ON CONFLICT DO NOTHING;

-- Step 3: Insert config values for username
INSERT INTO integration_config_values (id, integration_id, name, value, type, is_encrypted, created_at, updated_at, created_by, updated_by)
SELECT
  gen_random_uuid(),
  jc.id,
  'username',
  jc.username,
  'string',
  false,
  jc.created_at,
  jc.updated_at,
  jc.created_by,
  jc.updated_by
FROM jira_configurations jc
WHERE jc.username IS NOT NULL
ON CONFLICT DO NOTHING;

-- Step 4: Insert config values for password (encrypted)
INSERT INTO integration_config_values (id, integration_id, name, value, type, is_encrypted, created_at, updated_at, created_by, updated_by)
SELECT
  gen_random_uuid(),
  jc.id,
  'password',
  jc.password,
  'string',
  true,
  jc.created_at,
  jc.updated_at,
  jc.created_by,
  jc.updated_by
FROM jira_configurations jc
WHERE jc.password IS NOT NULL
ON CONFLICT DO NOTHING;

-- Step 5: Insert config values for auth_type
INSERT INTO integration_config_values (id, integration_id, name, value, type, is_encrypted, created_at, updated_at, created_by, updated_by)
SELECT
  gen_random_uuid(),
  jc.id,
  'auth_type',
  jc.auth_type,
  'string',
  false,
  jc.created_at,
  jc.updated_at,
  jc.created_by,
  jc.updated_by
FROM jira_configurations jc
WHERE jc.auth_type IS NOT NULL
ON CONFLICT DO NOTHING;

-- Step 6: Insert config values for projects (JSON)
INSERT INTO integration_config_values (id, integration_id, name, value, type, is_encrypted, created_at, updated_at, created_by, updated_by)
SELECT
  gen_random_uuid(),
  jc.id,
  'projects',
  jc.projects::text,
  'string',
  false,
  jc.created_at,
  jc.updated_at,
  jc.created_by,
  jc.updated_by
FROM jira_configurations jc
WHERE jc.projects IS NOT NULL
ON CONFLICT DO NOTHING;

-- Step 7: Insert config values for priorities (JSON)
INSERT INTO integration_config_values (id, integration_id, name, value, type, is_encrypted, created_at, updated_at, created_by, updated_by)
SELECT
  gen_random_uuid(),
  jc.id,
  'priorities',
  jc.priorities::text,
  'string',
  false,
  jc.created_at,
  jc.updated_at,
  jc.created_by,
  jc.updated_by
FROM jira_configurations jc
WHERE jc.priorities IS NOT NULL
ON CONFLICT DO NOTHING;

-- Step 8: Insert config values for users (JSON)
INSERT INTO integration_config_values (id, integration_id, name, value, type, is_encrypted, created_at, updated_at, created_by, updated_by)
SELECT
  gen_random_uuid(),
  jc.id,
  'users',
  jc.users::text,
  'string',
  false,
  jc.created_at,
  jc.updated_at,
  jc.created_by,
  jc.updated_by
FROM jira_configurations jc
WHERE jc.users IS NOT NULL
ON CONFLICT DO NOTHING;

-- Step 9: Insert config values for last_connected
INSERT INTO integration_config_values (id, integration_id, name, value, type, is_encrypted, created_at, updated_at, created_by, updated_by)
SELECT
  gen_random_uuid(),
  jc.id,
  'last_connected',
  jc.last_connected::text,
  'string',
  false,
  jc.created_at,
  jc.updated_at,
  jc.created_by,
  jc.updated_by
FROM jira_configurations jc
WHERE jc.last_connected IS NOT NULL
ON CONFLICT DO NOTHING;

-- Step 10: Update tickets table - rename column and update foreign key
ALTER TABLE tickets RENAME COLUMN configuration_id TO integration_id;

ALTER TABLE tickets DROP CONSTRAINT IF EXISTS tickets_configuration_id_fkey;

ALTER TABLE tickets ADD CONSTRAINT tickets_integration_id_fkey
  FOREIGN KEY (integration_id) REFERENCES integrations(id) ON DELETE RESTRICT;

-- NOTE: jira_configurations table will be kept temporarily for safety
-- It will be dropped in a future migration after validating everything works
