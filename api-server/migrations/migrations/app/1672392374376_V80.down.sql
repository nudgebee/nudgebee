
alter table "public"."recommendation" alter column "usage" drop not null;
alter table "public"."recommendation" add column "usage" json;

alter table "public"."recommendation"
  add constraint "recommendation_state_fkey"
  foreign key (state)
  references "public"."recommendation_status_type"
  (value) on update cascade on delete no action;
alter table "public"."recommendation" alter column "state" drop not null;
alter table "public"."recommendation" add column "state" text;

alter table "public"."recommendation" alter column "severity" drop not null;
alter table "public"."recommendation" add column "severity" text;

alter table "public"."recommendation"
  add constraint "recommendation_severity_fkey"
  foreign key ("severity")
  references "public"."recommendation_severity_type"
  ("value") on update cascade on delete no action;

alter table "public"."recommendation" alter column "usage_cost" drop not null;
alter table "public"."recommendation" add column "usage_cost" float8;

alter table "public"."recommendation" alter column "estimated_savings" drop not null;
alter table "public"."recommendation" add column "estimated_savings" float8;

alter table "public"."recommendation" alter column "cpu_utilization" drop not null;
alter table "public"."recommendation" add column "cpu_utilization" float8;

alter table "public"."recommendation" alter column "size" drop not null;
alter table "public"."recommendation" add column "size" text;
