-- Discord messaging integration moves from the legacy messaging_platforms table
-- to the integrations / integration_config_values store (bot token encrypted at
-- rest), matching Slack / MS Teams / Google Chat.
INSERT INTO integration_categories(value, description)
VALUES ('messaging', 'Chat and messaging platforms for bot interactions and notification delivery')
ON CONFLICT (value) DO NOTHING;

INSERT INTO integration_types(name, category, description)
VALUES ('discord', 'messaging',
        'Discord bot integration for notification delivery')
ON CONFLICT (name) DO NOTHING;
