
CREATE TABLE "public"."llm_conversation_feedback" ("id" serial NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp NOT NULL DEFAULT now(), "session_id" text NOT NULL, "module" text NOT NULL, "question" text NOT NULL, "llm_response" text NOT NULL, "user_corrected_response" text, "useful" boolean, "additional_notes" text, "conversation_id" text NOT NULL, "tenant_id" uuid NOT NULL, "cloud_account_id" uuid NOT NULL, "user_id" uuid NOT NULL, PRIMARY KEY ("id") );
CREATE OR REPLACE FUNCTION "public"."set_current_timestamp_updated_at"()
RETURNS TRIGGER AS $$
DECLARE
  _new record;
BEGIN
  _new := NEW;
  _new."updated_at" = NOW();
  RETURN _new;
END;
$$ LANGUAGE plpgsql;
CREATE TRIGGER "set_public_llm_conversation_feedback_updated_at"
BEFORE UPDATE ON "public"."llm_conversation_feedback"
FOR EACH ROW
EXECUTE PROCEDURE "public"."set_current_timestamp_updated_at"();
COMMENT ON TRIGGER "set_public_llm_conversation_feedback_updated_at" ON "public"."llm_conversation_feedback"
IS 'trigger to set value of column "updated_at" to current timestamp on row update';
