alter table llm_conversation_agent  
add column if not exists status text;

update llm_conversation_agent as lca
set status = 'fail'
where lca.response = ''
	or lca.response is null 
	or lca.response = '[]' 
	or lca.response = '{}' 
	or lca.response ilike 'agent not finished%' 
	or lca.response ilike 'agent:noaction%'
	or lca.response ilike '%"error"%'
	or lca.response ilike '%Bedrock Runtime:%'
	or lca.response ilike '%None is not a valid tool%'
	or lca.response ilike 'error:%'
	or lca.response ilike '%unable to fetch%'
	or lca.response ilike '%unfortunately%';
	
update llm_conversation_agent as lca
set status = 'success'
where status is null;


update llm_conversation_tool_calls
set status = 'fail'
where status = 'failed';




