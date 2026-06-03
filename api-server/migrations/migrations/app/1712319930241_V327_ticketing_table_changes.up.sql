

CREATE TABLE "public"."ticket_system_types" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."ticket_system_types"("value") VALUES (E'jira');

INSERT INTO "public"."ticket_system_types"("value") VALUES (E'github');

alter table "public"."ticket_system_types" rename to "ticket_tool_types";

alter table "public"."jira_configurations" add column "tool" text
 not null default 'jira';

alter table "public"."jira_configurations"
  add constraint "jira_configurations_tool_fkey"
  foreign key ("tool")
  references "public"."ticket_tool_types"
  ("value") on update no action on delete no action;

ALTER TABLE "public"."jira_configurations" ALTER COLUMN "tool" drop default;

alter table "public"."tickets" add column "account_id" uuid
 null;

alter table "public"."tickets"
  add constraint "tickets_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update cascade on delete cascade;

alter table "public"."tickets" drop constraint "tickets_platform_fkey";

UPDATE "public"."tickets" SET "platform" = 'jira' WHERE "platform" = 'JIRA';

alter table "public"."tickets"
  add constraint "tickets_platform_fkey"
  foreign key ("platform")
  references "public"."ticket_tool_types"
  ("value") on update no action on delete no action;

DROP table "public"."ticket_platforms";
