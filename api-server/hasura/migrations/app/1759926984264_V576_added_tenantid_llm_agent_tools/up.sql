CREATE INDEX IF NOT exists events_cloud_resource_id ON events (cloud_resource_id);

alter table llm_agents
add column if not exists tenant_id UUID;

alter table llm_tools
add column if not exists tenant_id UUID;

update llm_agents
set tenant_id = ca.tenant
from llm_agents_installation lgi
join cloud_accounts ca on ca.id = lgi.account_id
where llm_agents.id::text = lgi.agent_id::text and llm_agents.tenant_id is null;


update llm_tools 
set tenant_id = ca.tenant
from llm_tools_installation lgi
join cloud_accounts ca on ca.id = lgi.account_id
where llm_tools.id::text = lgi.tool_id::text and llm_tools.tenant_id is null;