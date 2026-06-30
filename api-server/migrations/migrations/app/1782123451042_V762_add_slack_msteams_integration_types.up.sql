-- Slack and MS Teams as messaging-category integration types. The 'messaging'
-- category is already seeded (V745). One-per-tenant is enforced in code at
-- install time (upsert by tenant+type), so no partial unique index here.
INSERT INTO integration_types(name, category, description)
VALUES
  ('slack', 'messaging', 'Slack workspace connected to a Nudgebee tenant for notification delivery'),
  ('ms_teams', 'messaging', 'Microsoft Teams connected to a Nudgebee tenant for notification delivery')
ON CONFLICT (name) DO NOTHING;
