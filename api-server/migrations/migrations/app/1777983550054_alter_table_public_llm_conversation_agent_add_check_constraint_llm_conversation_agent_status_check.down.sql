alter table "public"."llm_conversation_agent" drop constraint "llm_conversation_agent_status_check";
alter table "public"."llm_conversation_agent" add constraint "llm_conversation_agent_status_check" check (status = ANY (ARRAY['success'::text, 'fail'::text, 'in_progress'::text, 'waiting'::text, 'skipped'::text, 'needs_user_input'::text, 'waiting_for_client_tool'::text]));
