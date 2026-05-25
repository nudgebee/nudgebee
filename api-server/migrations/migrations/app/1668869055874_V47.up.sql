
alter table "public"."project_accounts" add column "allocation_start" timestamp
 null default now();

alter table "public"."project_accounts" add column "allocation_end" timestamp
 null;

alter table "public"."project_accounts" add column "allocation_pct" double precision
 null default '100';

alter table "public"."project_accounts" add constraint "allocation_pct_check" check (allocation_pct <= 100);

alter table "public"."project_accounts" add constraint "project_accounts_project_id_account_id_key" unique ("project_id", "account_id");

alter table "public"."project_accounts" add constraint "allocaion_end_check" check (allocation_end is null or allocation_end < allocation_start);

alter table "public"."project_accounts" add column "created_at" timestamp
 null default now();

alter table "public"."project_accounts" add column "updated_at" timestamp
 null default now();

alter table "public"."project_accounts" add column "created_by" uuid
 null;

alter table "public"."project_accounts" add column "updated_by" uuid
 null;

alter table "public"."project_accounts"
  add constraint "project_accounts_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."project_accounts"
  add constraint "project_accounts_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."project_cloud_resources"
  add constraint "project_cloud_resources_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."project_cloud_resources"
  add constraint "project_cloud_resources_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."project_cloud_resources" add constraint "allocation_pct_check" check (allocation_pct < 100);

alter table "public"."project_cloud_resources" add constraint "allocation_end_check" check (allocation_end is null or allocation_end > allocation_start);

alter table "public"."project_cloud_resources" drop constraint "allocation_pct_check";
alter table "public"."project_cloud_resources" add constraint "allocation_pct_check" check (allocation_pct <= 100::double precision);
