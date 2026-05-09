
alter table "public"."jira_configurations" add column "projects" json
 null;

alter table "public"."tickets" add column "project_key" text
 not null;

alter table "public"."tickets" alter column "project_key" set default 'DEMO';
