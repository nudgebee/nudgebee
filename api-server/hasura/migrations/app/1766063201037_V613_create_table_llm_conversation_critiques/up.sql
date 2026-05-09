CREATE TABLE "public"."llm_conversation_agent_critiques" (
	"id" uuid NOT NULL DEFAULT gen_random_uuid(), 
	"conversation_id" uuid NOT NULL, 
	"message_id" uuid NOT NULL, 
	"account_id" uuid NOT NULL, 
	"agent_name" text NOT NULL,
	"critiqued_content" text NOT NULL, 
        "input" text NOT NULL, 
	"critique_type" text NOT NULL, 
	"feedback" text, 
	"decision" text NOT NULL, 
	"created_at" timestamptz NOT NULL DEFAULT now(), 
PRIMARY KEY ("id") , FOREIGN KEY ("conversation_id") REFERENCES "public"."llm_conversations"("id") ON UPDATE cascade ON DELETE cascade, FOREIGN KEY ("message_id") REFERENCES "public"."llm_conversation_messages"("id") ON UPDATE cascade ON DELETE cascade);

COMMENT ON COLUMN "public"."llm_conversation_agent_critiques"."critique_type"
IS E'rewoo_planner critiques the plan, rewoo_solver critiques the final response, and react_answer critiques the agent''s execution actions.';


CREATE EXTENSION IF NOT EXISTS pgcrypto;
