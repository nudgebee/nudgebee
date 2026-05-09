
alter table "public"."notification_rules" add column "account_id" uuid
 null;

alter table "public"."notification_rules"
  add constraint "notification_rules_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update cascade on delete cascade;


UPDATE notification_rules
SET account_id = (
    SELECT id
    FROM cloud_accounts
    WHERE cloud_accounts.account_name = notification_rules.cluster
)
where notification_rules.account_id is null; 