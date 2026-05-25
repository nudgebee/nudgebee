
alter table "public"."llm_conversation_messages"
  add constraint "llm_conversation_messages_user_id_fkey"
  foreign key ("user_id")
  references "public"."users"
  ("id") on update restrict on delete restrict;
