DROP INDEX IF EXISTS public.idx_kb_source_path_unique;
ALTER TABLE public.llm_knowledgebases DROP CONSTRAINT IF EXISTS kb_source_org_only_check;
ALTER TABLE public.llm_knowledgebases DROP CONSTRAINT IF EXISTS llm_knowledgebases_source_id_fkey;
ALTER TABLE public.llm_knowledgebases DROP COLUMN IF EXISTS skill_meta;
ALTER TABLE public.llm_knowledgebases DROP COLUMN IF EXISTS files;
ALTER TABLE public.llm_knowledgebases DROP COLUMN IF EXISTS source_id;
DROP TABLE IF EXISTS public.llm_kb_skill_sources;
