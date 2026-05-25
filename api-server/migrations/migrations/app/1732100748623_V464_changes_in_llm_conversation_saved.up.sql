
alter table "public"."llm_conversation_saved" drop constraint "llm_conversation_saved_conversation_id_fkey";

alter table "public"."llm_conversation_saved" drop constraint "llm_conversation_saved_user_id_key";

alter table "public"."llm_conversation_saved" drop constraint "llm_conversation_saved_conversation_id_key";

BEGIN TRANSACTION;
ALTER TABLE "public"."llm_conversation_saved" DROP CONSTRAINT "llm_conversation_saved_pkey";

ALTER TABLE "public"."llm_conversation_saved"
    ADD CONSTRAINT "llm_conversation_saved_pkey" PRIMARY KEY ("id");
COMMIT TRANSACTION;

alter table "public"."llm_conversation_saved"
  add constraint "llm_conversation_saved_conversation_id_fkey"
  foreign key ("conversation_id")
  references "public"."llm_conversations"
  ("id") on update restrict on delete restrict;
