
alter table "public"."auto_pilot_reviewee" add column "account_id" uuid
 not null;

alter table "public"."auto_pilot_reviewers" add column "account_id" uuid
 not null;

alter table "public"."auto_pilot_approval_policy" add column "account_id" uuid
 not null;
