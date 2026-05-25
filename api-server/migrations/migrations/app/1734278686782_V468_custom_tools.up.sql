create table if not exists llm_tools (
	id uuid primary key,
	name text not null,
	description text not null,
	config json default '{}',
	input_schema json default '{}',
	"type" text not null,
	nb_tool_type text not null,
	status text not null,
	executor_type text not null,
	created_by uuid, 
	created_at timestamp default now(),
	updated_at timestamp default now()
);

create table if not exists llm_tools_installation (
	id uuid primary key,
	tool_id uuid not null,
	account_id uuid not null,
	config json default '{}',
	created_at timestamp default now(),
	created_by uuid
);

alter table public.llm_tools_installation
	drop constraint if exists llm_tools_installation_tool_account_unique;

ALTER TABLE public.llm_tools_installation 
	ADD CONSTRAINT llm_tools_installation_tool_account_unique 
		UNIQUE (tool_id,account_id);

alter table public.llm_tools
	drop constraint if exists llm_tools_check_type;	
	
ALTER TABLE public.llm_tools 
	ADD CONSTRAINT llm_tools_check_type 
		CHECK ("type" in ('system', 'custom'));

alter table public.llm_tools
	drop constraint if exists llm_tools_check_status;	
	
ALTER TABLE public.llm_tools 
	ADD CONSTRAINT llm_tools_check_status
		CHECK ("status" in ('enabled', 'disabled', 'draft'));
	
alter table public.llm_tools
	drop constraint if exists llm_tools_check_executortype;		

ALTER TABLE public.llm_tools 
	ADD CONSTRAINT llm_tools_check_executortype
		CHECK ("executor_type" in ('system', 'remote', 'runbook'));
