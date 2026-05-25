
INSERT INTO "public"."feature"("description", "value") VALUES (E'Analyzes your current Kubernetes cluster configuration and generates a step-by-step upgrade plan', E'UPGRADE_PLANNER') ON CONFLICT DO NOTHING;
