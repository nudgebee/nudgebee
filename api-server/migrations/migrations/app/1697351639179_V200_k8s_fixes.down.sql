
DELETE FROM "public"."recommendation_category_type" WHERE "value" = 'Configuration';

alter table "public"."cloud_account_score" drop constraint "cloud_account_score_tenant_cloud_account_id_source_key";
alter table "public"."cloud_account_score" add constraint "cloud_account_score_tenant_cloud_account_id_score_key" unique ("tenant", "cloud_account_id", "score");
