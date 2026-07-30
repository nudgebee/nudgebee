DELETE FROM public.feature_flag WHERE feature_id = 'TRIAGE_LLM_SCORING';
DELETE FROM public.feature WHERE value = 'TRIAGE_LLM_SCORING';
