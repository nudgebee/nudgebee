
alter table "public"."cloud_provider_type" rename to "cloud_provider";

alter table "public"."user_status_type" rename to "user_status_types";

alter table "public"."auth_type" rename to "auth_types";

alter table "public"."auth_provider_type" rename to "auth_provider_types";

alter table "public"."cloud_account_status_type" rename to "cloud_account_status_types";

alter table "public"."spends_resource_group_type" rename to "spends_resource_group_types";

DELETE FROM "public"."recommendation_status_type" WHERE "value" = 'Ignore';

DELETE FROM "public"."recommendation_status_type" WHERE "value" = 'Mitigated';

DELETE FROM "public"."recommendation_status_type" WHERE "value" = 'Open';

DELETE FROM "public"."recommendation_severity_type" WHERE "value" = 'Info';

DELETE FROM "public"."recommendation_severity_type" WHERE "value" = 'Low';

DELETE FROM "public"."recommendation_severity_type" WHERE "value" = 'Medium';

DELETE FROM "public"."recommendation_severity_type" WHERE "value" = 'High';
