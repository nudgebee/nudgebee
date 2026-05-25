
alter table "public"."funding_sources" add column "created_by" uuid
 null;

alter table "public"."funding_sources" add column "updated_by" uuid
 null;

alter table "public"."funding_sources"
  add constraint "funding_sources_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."funding_sources"
  add constraint "funding_sources_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."funding_sources" add constraint "funding_sources_tenant_name_key" unique ("tenant", "name");

alter table "public"."funding_sources" alter column "created_by" set not null;

alter table "public"."funding_sources" alter column "updated_by" set not null;

ALTER TABLE "public"."funding_sources" ALTER COLUMN "name" TYPE citext;
