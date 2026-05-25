


ALTER TABLE "public"."compliance_check_findings" ALTER COLUMN "created_at" TYPE timestamp;

ALTER TABLE "public"."compliance_check_findings" ALTER COLUMN "updated_at" TYPE timestamp;

alter table "public"."compliance_check" add column "policy" text
 not null;

alter table "public"."compliance_check_findings" add column "description" varchar
 null;

alter table "public"."compliance_check_findings" drop column "description" cascade;

alter table "public"."compliance_check_findings" add column "description_" text
 null default 'json';

ALTER TABLE "public"."compliance_check_findings" ALTER COLUMN "description_" drop default;

alter table "public"."compliance_check_findings" rename column "description_" to "description";

CREATE TABLE "public"."cloud_provider" ("value" text NOT NULL, "comment" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."cloud_provider"("value", "comment") VALUES (E'AWS', E'AWS');

INSERT INTO "public"."cloud_provider"("value", "comment") VALUES (E'GCP', E'GCP');

INSERT INTO "public"."cloud_provider"("value", "comment") VALUES (E'Azure', E'Azure');

insert into "public"."cloud_provider" ("value", "comment") values ('Other', 'Other'), ('K8s', 'K8s'), ('OpenAi', 'OpenAi'), ('Snowflake', 'Snowflake'), ('NewRelic', 'NewRelic'), ('Slack', 'Slack'), ('Jira', 'Jira');

alter table "public"."compliance_check"
  add constraint "compliance_check_cloud_provider_fkey"
  foreign key ("cloud_provider")
  references "public"."cloud_provider"
  ("value") on update restrict on delete restrict;

CREATE TABLE "public"."compliance_check_type" ("text" text NOT NULL, "comment" Text NOT NULL, PRIMARY KEY ("text") );

INSERT INTO "public"."compliance_check_type"("text", "comment") VALUES (E'Cloud Custodian', E'Cloud Custodian');

alter table "public"."compliance_check"
  add constraint "compliance_check_check_type_fkey"
  foreign key ("check_type")
  references "public"."compliance_check_type"
  ("text") on update restrict on delete restrict;
