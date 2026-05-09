alter table "public"."llm_conversation_tool_calls" drop constraint "llm_conversation_tool_calls_status_check";
alter table "public"."llm_conversation_tool_calls" add constraint "llm_conversation_tool_calls_status_check" check (status = ANY (ARRAY['success'::text, 'fail'::text, 'in_progress'::text, 'error'::text, 'waiting'::text, 'waiting_for_client'::text]));
