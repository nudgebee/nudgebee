
alter table "public"."llm_conversation_saved" drop constraint "llm_conversation_saved_conversation_id_fkey";

alter table "public"."llm_conversation_saved" drop constraint "llm_conversation_saved_pkey";
alter table "public"."llm_conversation_saved"
    add constraint "llm_conversation_saved_pkey"
    primary key ("id", "conversation_id", "user_id");

alter table "public"."llm_conversation_saved" add constraint "llm_conversation_saved_conversation_id_key" unique ("conversation_id");

alter table "public"."llm_conversation_saved" add constraint "llm_conversation_saved_user_id_key" unique ("user_id");

alter table "public"."llm_conversation_saved"
  add constraint "llm_conversation_saved_conversation_id_fkey"
  foreign key ("conversation_id")
  references "public"."llm_conversations"
  ("id") on update restrict on delete restrict;
