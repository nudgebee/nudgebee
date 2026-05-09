
CREATE TABLE "public"."llm_conversation_saved" ("id" uuid NOT NULL, "conversation_id" uuid NOT NULL, "user_id" uuid NOT NULL, PRIMARY KEY ("id","conversation_id","user_id") , FOREIGN KEY ("conversation_id") REFERENCES "public"."llm_conversations"("id") ON UPDATE restrict ON DELETE restrict, FOREIGN KEY ("user_id") REFERENCES "public"."users"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("conversation_id"), UNIQUE ("user_id"));COMMENT ON TABLE "public"."llm_conversation_saved" IS E'Saved conversation of user';

alter table "public"."llm_conversation_saved" add column "created_At" timestamptz
 not null default now();

ALTER TABLE "public"."llm_conversation_saved" ALTER COLUMN "created_At" TYPE timestamp;
alter table "public"."llm_conversation_saved" rename column "created_At" to "created_at";

alter table "public"."llm_conversation_saved" alter column "id" set default gen_random_uuid();
