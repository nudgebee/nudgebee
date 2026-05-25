
alter table "public"."applications_grouping" alter column "updated_by" set not null;

alter table "public"."applications_grouping" alter column "created_by" set not null;

alter table "public"."applications_grouping" alter column "updated_at" set not null;

alter table "public"."applications_grouping" alter column "created_at" set not null;

DROP TABLE "public"."applications_grouping";
