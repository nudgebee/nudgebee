alter table "public"."llm_conversation_tool_calls" drop constraint "llm_conversation_tool_calls_tool_name_conversation_id_message_id_tool_id_agent_id_key";
alter table "public"."llm_conversation_tool_calls" add constraint "llm_conversation_tool_calls_tool_name_conversation_id_message_id_tool_id_key" unique ("tool_name", "conversation_id", "message_id", "tool_id");
