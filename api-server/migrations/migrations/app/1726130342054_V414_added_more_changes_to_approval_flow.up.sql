
alter table "public"."auto_pilot_approvals" add column "auto_pilot_type" text
 not null;

CREATE TABLE "public"."auto_pilot_type" ("type" text NOT NULL, "description" text NOT NULL, PRIMARY KEY ("type") , UNIQUE ("type"));COMMENT ON TABLE "public"."auto_pilot_type" IS E'type on auto pilot';

INSERT INTO "public"."auto_pilot_type"("type", "description") VALUES (E'runbook', E'auto pilot of type runbook');

INSERT INTO "public"."auto_pilot_type"("type", "description") VALUES (E'auto_optimize', E'auto pilot of type auto optimize');

alter table "public"."auto_pilot_approvals"
  add constraint "auto_pilot_approvals_auto_pilot_type_fkey"
  foreign key ("auto_pilot_type")
  references "public"."auto_pilot_type"
  ("type") on update restrict on delete restrict;

CREATE TABLE "public"."auto_pilot_approval_status" ("status" text NOT NULL, "description" text, PRIMARY KEY ("status") );COMMENT ON TABLE "public"."auto_pilot_approval_status" IS E'enum for auto pilot approval status';

INSERT INTO "public"."auto_pilot_approval_status"("status", "description") VALUES (E'PENDING', E'the approval is pending');

INSERT INTO "public"."auto_pilot_approval_status"("status", "description") VALUES (E'APPROVED', E'the approval is approved');

INSERT INTO "public"."auto_pilot_approval_status"("status", "description") VALUES (E'REJECTED', E'the approval is rejected');

alter table "public"."auto_pilot_approvals"
  add constraint "auto_pilot_approvals_status_fkey"
  foreign key ("status")
  references "public"."auto_pilot_approval_status"
  ("status") on update restrict on delete restrict;
