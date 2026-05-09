
DROP TABLE "public"."user_auths";

DELETE FROM "public"."auth_types" WHERE "value" = 'email';

DELETE FROM "public"."auth_types" WHERE "value" = 'token';

DELETE FROM "public"."auth_types" WHERE "value" = 'google';

DROP TABLE "public"."auth_types";

DELETE FROM "public"."auth_provider_types" WHERE "value" = 'email';

DELETE FROM "public"."auth_provider_types" WHERE "value" = 'credentials';

DELETE FROM "public"."auth_provider_types" WHERE "value" = 'oauth';

DROP TABLE "public"."auth_provider_types";

DROP TABLE "public"."project_users";

DROP TABLE "public"."businessunit_users";

DROP TABLE "public"."user_attrs";

DROP TABLE "public"."projects";

DROP TABLE "public"."business_unit";

alter table "public"."tenant" drop constraint "tenant_updated_by_fkey";

alter table "public"."tenant" drop constraint "tenant_created_by_fkey";

alter table "public"."users" drop constraint "users_updated_by_fkey";

alter table "public"."users" drop constraint "users_created_by_fkey";

alter table "public"."users" drop constraint "users_status_fkey";

DROP TABLE "public"."users";

DELETE FROM "public"."user_status_types" WHERE "value" = 'suspended';

DELETE FROM "public"."user_status_types" WHERE "value" = 'inactive';

DELETE FROM "public"."user_status_types" WHERE "value" = 'active';

ALTER TABLE "public"."user_status_types" ALTER COLUMN "comment" TYPE character varying;

ALTER TABLE "public"."user_status_types" ALTER COLUMN "value" TYPE character varying;

DROP TABLE "public"."user_status_types";

DROP TABLE "public"."tenant";
