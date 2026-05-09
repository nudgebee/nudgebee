
CREATE TABLE "public"."compliance_check_finding_status_type" ("value" text NOT NULL, "description" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."compliance_check_finding_status_type"("value", "description") VALUES (E'OPEN', E'OPEN');

INSERT INTO "public"."compliance_check_finding_status_type"("value", "description") VALUES (E'SUPPRESSED', E'SUPPRESSED');

INSERT INTO "public"."compliance_check_finding_status_type"("value", "description") VALUES (E'MITIGATED', E'MITIGATED');

alter table "public"."compliance_check_findings"
  add constraint "compliance_check_findings_status_fkey"
  foreign key ("status")
  references "public"."compliance_check_finding_status_type"
  ("value") on update restrict on delete restrict;
