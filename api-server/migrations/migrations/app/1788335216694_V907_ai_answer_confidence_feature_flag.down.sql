-- Remove all per-tenant enrolments first (FK constraint), then the catalog row.
DELETE FROM public.feature_flag WHERE feature_id = 'AI_ANSWER_CONFIDENCE';
DELETE FROM public.feature WHERE value = 'AI_ANSWER_CONFIDENCE';
