
INSERT INTO "public"."feature"("description", "value") VALUES (E'Workflow automation feature', E'WORKFLOWS') ON CONFLICT (value) DO NOTHING;
