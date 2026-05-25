alter table "public"."llm_conversations"
  add constraint "llm_conversations_account_id_fkey"
  foreign key ("account_id")
  references "public"."cloud_accounts"
  ("id") on update restrict on delete restrict;
