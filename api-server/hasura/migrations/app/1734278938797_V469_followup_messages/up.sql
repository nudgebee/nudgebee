alter table llm_conversation_messages 
add column if not exists parent_agent_id uuid;

alter table llm_conversation_messages
add column if not exists message_config text;

alter table llm_conversation_messages
add column if not exists message_context text;
