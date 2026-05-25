alter type llm_conversation_status
add value if not exists 'KILLED';

alter type llm_conversation_status
add value if not exists 'PENDING';

alter table llm_conversation_messages
add column if not exists worker_name text;

alter table llm_conversation_messages
add column if not exists agent_name text;

ALTER TABLE public.llm_conversation_agent ALTER COLUMN agent_name SET NOT NULL;
ALTER TABLE public.llm_conversation_agent ALTER COLUMN message_id SET NOT NULL;
ALTER TABLE public.llm_conversation_agent ALTER COLUMN created_at SET NOT NULL;
ALTER TABLE public.llm_conversation_agent ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE public.llm_conversation_agent ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE public.llm_conversation_agent ALTER COLUMN conversation_id SET NOT NULL;
ALTER TABLE public.llm_conversation_agent ALTER COLUMN status SET NOT NULL;


ALTER TABLE public.llm_conversation_messages ALTER COLUMN conversation_id SET NOT NULL;
ALTER TABLE public.llm_conversation_messages ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE public.llm_conversation_messages ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE public.llm_conversation_messages ALTER COLUMN updated_at SET NOT NULL;
ALTER TABLE public.llm_conversation_messages ALTER COLUMN "role" SET NOT NULL;
ALTER TABLE public.llm_conversation_messages ALTER COLUMN message_type SET NOT NULL;
ALTER TABLE public.llm_conversation_messages ALTER COLUMN status SET NOT NULL;


ALTER TABLE public.llm_conversations ALTER COLUMN status SET NOT NULL;
ALTER TABLE public.llm_conversations ALTER COLUMN updated_at SET NOT NULL;


create table if not exists  nb_workers(	
	worker_type text not null,
	worker_name text not null,
	is_leader boolean default false,
	updated_at timestamp default now(),	
	primary key (worker_type, worker_name)
);



