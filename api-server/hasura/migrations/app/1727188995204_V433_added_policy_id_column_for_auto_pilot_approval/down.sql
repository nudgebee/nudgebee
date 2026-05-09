
alter table "public"."auto_pilot_reviewers" drop constraint "auto_pilot_reviewers_policy_id_fkey";

alter table "public"."auto_pilot_reviewee" drop constraint "auto_pilot_reviewee_policy_id_fkey";

alter table "public"."auto_pilot_reviewee" alter column "policy_id" drop not null;

alter table "public"."auto_pilot_reviewers" alter column "policy_id" drop not null;
