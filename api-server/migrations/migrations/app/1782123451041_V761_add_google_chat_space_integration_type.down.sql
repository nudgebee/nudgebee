DROP INDEX IF EXISTS integrations_google_chat_space_name_uk;

-- Remove any existing space bindings first. integrations.type FK-references
-- integration_types(name) (integrations_type_fkey), so the integration_types
-- row cannot be deleted while google_chat_space rows still reference it; and
-- integration_config_values FK-references integrations(id), so clear it first.
DELETE FROM integration_config_values
WHERE integration_id IN (SELECT id FROM integrations WHERE type = 'google_chat_space');

DELETE FROM integrations WHERE type = 'google_chat_space';

DELETE FROM integration_types WHERE name = 'google_chat_space';

-- Only remove the messaging category if no other integration_types reference it.
DELETE FROM integration_categories
WHERE value = 'messaging'
  AND NOT EXISTS (SELECT 1 FROM integration_types WHERE category = 'messaging');
