
CREATE  INDEX "recommendation_tenant" on
  "public"."recommendation" using btree ("tenant_id");

CREATE  INDEX "recommendation_tenant_resource" on
  "public"."recommendation" using btree ("tenant_id", "resource_id");
