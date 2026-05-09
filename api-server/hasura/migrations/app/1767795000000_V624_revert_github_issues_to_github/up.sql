INSERT INTO integration_types (name, description)
SELECT 'github', 'GitHub Integration'
    WHERE NOT EXISTS (
  SELECT 1 FROM integration_types WHERE name = 'github'
);

UPDATE integrations
SET type = 'github'
WHERE type = 'github_issues';

DELETE FROM integration_types
WHERE name = 'github_issues'
  AND NOT EXISTS (
    SELECT 1 FROM integrations WHERE type = 'github_issues'
);
