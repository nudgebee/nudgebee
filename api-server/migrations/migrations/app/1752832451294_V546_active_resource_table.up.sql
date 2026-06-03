
CREATE TABLE "public"."active_resources" ("cloud_account_id" uuid NOT NULL, "tenant_id" uuid NOT NULL, "external_resource_id" text NOT NULL, "resourse_id" text NOT NULL, "resource_type" text NOT NULL, "updated_at" timestamp NOT NULL DEFAULT now(), PRIMARY KEY ("cloud_account_id","external_resource_id","tenant_id","resource_type") );

CREATE  INDEX "idx_active_resources_account_tenant_type" on
  "public"."active_resources" using btree ("cloud_account_id", "tenant_id", "resource_type");
