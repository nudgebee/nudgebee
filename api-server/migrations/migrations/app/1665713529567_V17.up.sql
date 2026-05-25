
CREATE TABLE "public"."compliance_check_status_type" ("value" text NOT NULL, "comment" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."compliance_check_status_type"("value", "comment") VALUES (E'active', E'active');

INSERT INTO "public"."compliance_check_status_type"("value", "comment") VALUES (E'disabled', E'disabled');

alter table "public"."compliance_check" add column "status" text
 not null default 'active';

alter table "public"."compliance_check"
  add constraint "compliance_check_status_fkey"
  foreign key ("status")
  references "public"."compliance_check_status_type"
  ("value") on update restrict on delete restrict;
