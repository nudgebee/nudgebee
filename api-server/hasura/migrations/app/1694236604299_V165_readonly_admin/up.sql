delete from "public"."roles" where "value" != 'tenant_admin';

INSERT INTO "public"."roles"("display_name", "value") VALUES (E'ReadOnly Admin', E'tenant_admin_readonly');
