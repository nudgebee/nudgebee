
alter table "public"."auto_pilot_task" add column "account_id" uuid
 null;

update
    auto_pilot_task as apt
set
    account_id = (
        select
            account_id
        from
            auto_pilot ap
        where
            ap.id = apt.auto_pilot_id
    );

alter table "public"."auto_pilot_task" alter column "account_id" set not null;

alter table "public"."auto_pilot_task"
  add constraint "auto_pilot_task_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;
