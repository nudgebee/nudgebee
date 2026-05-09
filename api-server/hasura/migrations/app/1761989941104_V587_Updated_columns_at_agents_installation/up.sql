
alter table "public"."llm_agents_installation" add column "updated_at" timestamp
 null;

alter table "public"."llm_agents_installation" add column "updated_by" uuid
 null;
