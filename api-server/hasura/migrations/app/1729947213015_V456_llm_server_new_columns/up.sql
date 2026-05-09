alter table llm_conversation_agent 
add column if not exists query_context text;

alter table llm_conversation_agent 
add column if not exists query_config text;

alter table llm_conversation_tool_calls 
add column if not exists status text;

CREATE  INDEX if not exists "events_starts_idx" on
  "public"."events" using btree ("starts_at");

update llm_conversation_tool_calls c
set status = 'fail'
where c.response in ('error: unable to fetch data', '[]', '', '{}', 'null', 'no data found', '{"result":[]}');

update llm_conversation_tool_calls c
set status = 'success'
where c.response not in ('error: unable to fetch data', '[]', '', '{}', 'null', 'no data found', '{"result":[]}');





