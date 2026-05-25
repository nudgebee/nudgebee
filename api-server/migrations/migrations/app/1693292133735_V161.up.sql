
CREATE  INDEX "cloud_resourses_tenant_account" on
  "public"."cloud_resourses" using btree ("tenant", "account");

CREATE  INDEX "spends_tenant" on
  "public"."spends" using btree ("tenant");

CREATE  INDEX "spends_cloud_account" on
  "public"."spends" using btree ("cloud_account");

CREATE  INDEX "spends_cloud_resource_id" on
  "public"."spends" using btree ("cloud_resource_id");
