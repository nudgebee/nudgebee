INSERT INTO integration_types (name, description)
SELECT 'github_issues', 'GitHub Issues Integration'
    WHERE NOT EXISTS (
  SELECT 1 FROM integration_types WHERE name = 'github_issues'
);

UPDATE integrations
SET type = 'github_issues'
WHERE type = 'github';

DELETE FROM integration_types
WHERE name = 'github'
  AND NOT EXISTS (
    SELECT 1 FROM integrations WHERE type = 'github'
);
