
alter table "public"."recommendation" drop column "size" cascade;

alter table "public"."recommendation" drop column "cpu_utilization" cascade;

alter table "public"."recommendation" drop column "estimated_savings" cascade;

alter table "public"."recommendation" drop column "usage_cost" cascade;

alter table "public"."recommendation" drop constraint "recommendation_severity_fkey";

alter table "public"."recommendation" drop column "severity" cascade;

alter table "public"."recommendation" drop column "state" cascade;

alter table "public"."recommendation" drop column "usage" cascade;
