ALTER TABLE public.workflows
ADD COLUMN created_from_session_id varchar(255) NULL;

COMMENT ON COLUMN public.workflows.created_from_session_id IS
'LLM conversation session_id that produced this workflow (set on create only). NULL for UI/manual workflows. UI uses this to deep-link back to the originating chat.';
