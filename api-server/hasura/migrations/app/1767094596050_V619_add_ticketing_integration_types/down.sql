-- Remove ticketing integration types
DELETE FROM integration_types WHERE name IN ('jira', 'github_issues', 'servicenow', 'pagerduty');

-- Remove ticketing category
DELETE FROM integration_categories WHERE value = 'ticketing';
