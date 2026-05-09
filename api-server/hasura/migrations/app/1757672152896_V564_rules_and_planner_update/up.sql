

INSERT INTO "public"."notification_source_type"("value") VALUES (E'cloud') ON CONFLICT DO NOTHING;


INSERT INTO "public"."feature"("description", "value") VALUES (E'Analyzes your current Kubernetes cluster configuration and generates a step-by-step upgrade plan', E'UPGRADE_PLANNER') ON CONFLICT DO NOTHING;


INSERT INTO "public"."feature"("description", "value") VALUES (E'Get root cause analysis for events', E'GENERATE_RCA') ON CONFLICT DO NOTHING;

alter table "public"."notification_rules" drop column "severity_levels" cascade;

alter table "public"."notification_rules" add column "severity" text
 null;

alter table "public"."upgrade_plan_audit" add column "comments" text
 null;
