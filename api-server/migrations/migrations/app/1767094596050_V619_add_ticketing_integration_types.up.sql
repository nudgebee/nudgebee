-- Add ticketing category to integration_categories
INSERT INTO integration_categories (value, description)
VALUES ('ticketing', 'Ticketing and issue tracking systems')
ON CONFLICT (value) DO NOTHING;

-- Add ticketing integration types to integration_types
INSERT INTO integration_types (name, category, description) VALUES
  ('jira', 'ticketing', 'Jira issue tracking'),
  ('github_issues', 'ticketing', 'GitHub Issues tracking'),
  ('servicenow', 'ticketing', 'ServiceNow incident management'),
  ('pagerduty', 'ticketing', 'PagerDuty incident management')
ON CONFLICT (name) DO NOTHING;
