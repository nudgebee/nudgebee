
alter table "public"."cloud_resourses" add constraint "cloud_resourses_account_arn_key" unique ("account", "arn");

alter table "public"."spends" add column "cloud_resource_id" uuid
 null;

alter table "public"."spends"
  add constraint "spends_cloud_resource_id_fkey"
  foreign key ("cloud_resource_id")
  references "public"."cloud_resourses"
  ("id") on update restrict on delete restrict;
