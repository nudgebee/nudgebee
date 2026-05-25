

CREATE TABLE "public"."account_purpose_type" ("value" text NOT NULL, PRIMARY KEY ("value") , UNIQUE ("value"));

INSERT INTO "public"."account_purpose_type"("value") VALUES (E'DEV');

INSERT INTO "public"."account_purpose_type"("value") VALUES (E'PROD');

INSERT INTO "public"."account_purpose_type"("value") VALUES (E'QA');

INSERT INTO "public"."account_purpose_type"("value") VALUES (E'UAT');



alter table "public"."severity_enum" rename to "recommendation_severity";

alter table "public"."state_enum" rename to "recommendation__state";

alter table "public"."cloud_resourses" drop column "tags" cascade;

alter table "public"."cloud_resourses" add column "tags" json
 null;

alter table "public"."cloud_accounts" add column account_purpose text NULL;

alter table "public"."cloud_accounts" add column account_access text NULL;

alter table "public"."cloud_accounts"  add column "data" jsonb NULL;