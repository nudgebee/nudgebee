
INSERT INTO "public"."recommendation_severity_type"("value") VALUES (E'High');

INSERT INTO "public"."recommendation_severity_type"("value") VALUES (E'Medium');

INSERT INTO "public"."recommendation_severity_type"("value") VALUES (E'Low');

INSERT INTO "public"."recommendation_severity_type"("value") VALUES (E'Info');

INSERT INTO "public"."recommendation_status_type"("value") VALUES (E'Open');

INSERT INTO "public"."recommendation_status_type"("value") VALUES (E'Mitigated');

INSERT INTO "public"."recommendation_status_type"("value") VALUES (E'Ignore');

alter table "public"."spends_resource_group_types" rename to "spends_resource_group_type";

alter table "public"."cloud_account_status_types" rename to "cloud_account_status_type";

alter table "public"."auth_provider_types" rename to "auth_provider_type";

alter table "public"."auth_types" rename to "auth_type";

alter table "public"."user_status_types" rename to "user_status_type";

alter table "public"."cloud_provider" rename to "cloud_provider_type";
