
alter table "public"."compliance_check_findings" drop constraint "compliance_check_findings_status_fkey";

DELETE FROM "public"."compliance_check_finding_status_type" WHERE "value" = 'MITIGATED';

DELETE FROM "public"."compliance_check_finding_status_type" WHERE "value" = 'SUPPRESSED';

DELETE FROM "public"."compliance_check_finding_status_type" WHERE "value" = 'OPEN';

DROP TABLE "public"."compliance_check_finding_status_type";
