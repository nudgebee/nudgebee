
alter table "public"."notification_rule_mappings" add column "platform_installation_id" uuid
 null;

alter table "public"."notification_rule_mappings"
  add constraint "notification_rule_mappings_platform_installation_id_fkey"
  foreign key ("platform_installation_id")
  references "public"."messaging_platforms"
  ("id") on update cascade on delete cascade;
