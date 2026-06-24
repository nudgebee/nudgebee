-- Category for chat / messaging platform integrations. Each platform still
-- has its own integration shape — Slack / MS Teams retain their channel-picker
-- UI when they migrate; Google Chat introduces the chat-side binding model here.
INSERT INTO integration_categories(value, description)
VALUES ('messaging', 'Chat and messaging platforms for bot interactions and notification delivery')
ON CONFLICT (value) DO NOTHING;

INSERT INTO integration_types(name, category, description)
VALUES ('google_chat_space', 'messaging',
        'Google Chat space bound to a Nudgebee tenant for bot routing')
ON CONFLICT (name) DO NOTHING;

-- Google Chat space IDs (spaces/XYZ) are globally unique on Google's side;
-- the partial unique index keeps them globally unique on ours so two tenants
-- cannot claim the same space.
CREATE UNIQUE INDEX IF NOT EXISTS integrations_google_chat_space_name_uk
  ON integrations (name) WHERE type = 'google_chat_space';
