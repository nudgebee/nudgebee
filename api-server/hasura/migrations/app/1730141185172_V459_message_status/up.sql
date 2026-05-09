alter table llm_conversation_messages  
add column if not exists status llm_conversation_status;

update llm_conversation_messages
set status = lc.status
from llm_conversations lc
where lc.id =  conversation_id;

alter table llm_conversations 
add column if not exists title text;

update llm_conversations lc
set title = lcm.message 
from llm_conversation_messages lcm
where lc.id = lcm.conversation_id;

alter table llm_conversation_agent
drop constraint if exists llm_conversation_agent_status_check;

alter table llm_conversation_agent
add constraint  llm_conversation_agent_status_check
check (status in ('success', 'fail', 'in_progress'));

alter table llm_conversation_tool_calls
drop constraint if exists llm_conversation_tool_calls_status_check;

alter table llm_conversation_tool_calls
add constraint llm_conversation_tool_calls_status_check
CHECK (status in ('success', 'fail', 'in_progress'));