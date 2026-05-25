
alter table "public"."cloud_resourses" add constraint "cloud_resourses_account_resourse_id_type_region_key" unique ("account", "resourse_id", "type", "region");
