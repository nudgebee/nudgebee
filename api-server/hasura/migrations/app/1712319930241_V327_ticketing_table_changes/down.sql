
-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- DROP table "public"."ticket_platforms";

alter table "public"."tickets" drop constraint "tickets_platform_fkey";

alter table "public"."tickets"
  add constraint "tickets_platform_fkey"
  foreign key ("platform")
  references "public"."ticket_platforms"
  ("value") on update no action on delete no action;


alter table "public"."tickets" drop constraint "tickets_account_id_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."tickets" add column "account_id" uuid
--  null;

alter table "public"."jira_configurations" alter column "tool" set default 'jira'::text;

alter table "public"."jira_configurations" drop constraint "jira_configurations_tool_fkey";

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."jira_configurations" add column "tool" text
--  not null default 'jira';

alter table "public"."ticket_tool_types" rename to "ticket_system_types";

DELETE FROM "public"."ticket_system_types" WHERE "value" = 'github';

DELETE FROM "public"."ticket_system_types" WHERE "value" = 'jira';

DROP TABLE "public"."ticket_system_types";
