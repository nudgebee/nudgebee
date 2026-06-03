create table if not exists llm_agents (
	id uuid primary key,
	name text not null,
	description text not null,
	config json default '{}',
	"type" text not null,
	status text not null,
	system_prompt text not null,
	system_prompt_variables json,
	executor_type text not null,
	tools json,
	created_by uuid, 
	created_at timestamp default now(),
	updated_at timestamp default now()
);

create table if not exists llm_agents_installation (
	id uuid primary key,
	agent_id uuid not null,
	account_id uuid not null,
	config json default '{}',
	created_at timestamp default now(),
	created_by uuid
);

alter table public.llm_agents_installation
	drop constraint if exists llm_agents_installation_agent_account_unique;

ALTER TABLE public.llm_agents_installation 
	ADD CONSTRAINT llm_agents_installation_agent_account_unique 
		UNIQUE (agent_id,account_id);


alter table public.llm_agents
	drop constraint if exists llm_agents_check_type;		

ALTER TABLE public.llm_agents 
	ADD CONSTRAINT llm_agents_check_type 
		CHECK ("type" in ('system', 'custom'));


alter table public.llm_agents
	drop constraint if exists llm_agents_check_status;

ALTER TABLE public.llm_agents 
	ADD CONSTRAINT llm_agents_check_status
		CHECK ("status" in ('enabled', 'disabled', 'draft'));


alter table public.llm_agents
	drop constraint if exists llm_agents_check_executortype;
ALTER TABLE public.llm_agents 
	ADD CONSTRAINT llm_agents_check_executortype
		CHECK ("executor_type" in ('tools', 'react', 'rewoo', 'custom'));
