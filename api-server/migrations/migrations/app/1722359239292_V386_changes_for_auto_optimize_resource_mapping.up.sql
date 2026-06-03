
alter table "public"."auto_optimize_resource_map" add column "tenant_id" uuid
 not null;

alter table "public"."auto_optimize_resource_map" add column "account_id" uuid
 not null;

alter table "public"."auto_optimize_resource_map"
  add constraint "auto_optimize_resource_map_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;

alter table "public"."auto_optimize_resource_map"
  add constraint "auto_optimize_resource_map_account_id_fkey2"
  foreign key ("account_id")
  references "public"."tenant"
  ("id") on update restrict on delete restrict;

alter table "public"."auto_optimize_resource_map" drop constraint "auto_optimize_resource_map_account_id_fkey2",
  add constraint "auto_optimize_resource_map_tenant_id_fkey"
  foreign key ("tenant_id")
  references "public"."tenant"
  ("id") on update restrict on delete restrict;
