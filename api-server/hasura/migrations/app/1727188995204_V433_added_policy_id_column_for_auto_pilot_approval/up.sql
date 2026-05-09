
alter table "public"."auto_pilot_reviewers" alter column "policy_id" set not null;

alter table "public"."auto_pilot_reviewee" alter column "policy_id" set not null;

alter table "public"."auto_pilot_reviewee"
  add constraint "auto_pilot_reviewee_policy_id_fkey"
  foreign key ("policy_id")
  references "public"."auto_pilot_approval_policy"
  ("id") on update restrict on delete restrict;

alter table "public"."auto_pilot_reviewers"
  add constraint "auto_pilot_reviewers_policy_id_fkey"
  foreign key ("policy_id")
  references "public"."auto_pilot_approval_policy"
  ("id") on update restrict on delete restrict;
