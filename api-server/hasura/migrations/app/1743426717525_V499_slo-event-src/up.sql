INSERT INTO "public"."event_source" ("value") VALUES (E'slo') ON CONFLICT ("value") DO NOTHING;
