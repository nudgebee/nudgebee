
INSERT INTO "public"."event_source"("value") VALUES (E'newrelic_webhook') ON CONFLICT DO NOTHING;
