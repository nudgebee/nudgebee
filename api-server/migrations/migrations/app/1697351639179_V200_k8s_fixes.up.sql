
alter table "public"."cloud_account_score" drop constraint if exists "cloud_account_score_cloud_account_id_tenant_score_key";
alter table "public"."cloud_account_score" add constraint "cloud_account_score_tenant_cloud_account_id_source_key" unique ("tenant", "cloud_account_id", "source");

INSERT INTO "public"."recommendation_category_type"("value") VALUES (E'Configuration');
