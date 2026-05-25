
alter table "public"."agent" add column "agent_secret_v2" text
 null;

alter table "public"."agent" rename column "agent_secret_v2" to "access_secret_v2";
