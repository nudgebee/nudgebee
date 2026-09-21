-- 'ai_cost' backs the AI Cost Daily Report notification rule
-- (notifications_server.services.rules.TENANT_WIDE_SOURCES) but was never
-- seeded here, so every save of that rule fails notification_rules_source_fkey.
INSERT INTO "public"."notification_source_type"("value") VALUES ('ai_cost') ON CONFLICT DO NOTHING;
