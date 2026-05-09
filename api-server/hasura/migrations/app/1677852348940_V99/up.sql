
alter table "public"."compliance_rules" add column "remediation" text
 null;

alter table "public"."compliance_rules" add column "rationale" text
 null;

alter table "public"."compliance_rules" add column "audit" text
 null;
