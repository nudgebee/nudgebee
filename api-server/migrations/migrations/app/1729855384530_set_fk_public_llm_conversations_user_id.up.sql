alter table "public"."llm_conversations"
  add constraint "llm_conversations_user_id_fkey"
  foreign key ("user_id")
  references "public"."users"
  ("id") on update restrict on delete restrict;
