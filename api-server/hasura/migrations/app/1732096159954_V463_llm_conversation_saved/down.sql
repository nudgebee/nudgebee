
ALTER TABLE "public"."llm_conversation_saved" ALTER COLUMN "id" drop default;

alter table "public"."llm_conversation_saved" rename column "created_at" to "created_At";
ALTER TABLE "public"."llm_conversation_saved" ALTER COLUMN "created_At" TYPE timestamp with time zone;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- alter table "public"."llm_conversation_saved" add column "created_At" timestamptz
--  not null default now();

DROP TABLE "public"."llm_conversation_saved";
