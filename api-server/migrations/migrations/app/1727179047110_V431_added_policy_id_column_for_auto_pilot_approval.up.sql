
alter table "public"."auto_pilot_reviewers" add column "policy_id" uuid
 null;

alter table "public"."auto_pilot_reviewee" add column "policy_id" uuid
 null;
