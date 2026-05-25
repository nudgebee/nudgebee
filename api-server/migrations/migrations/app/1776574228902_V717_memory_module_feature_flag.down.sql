-- Remove all per-tenant enrolments first (FK constraint), then the catalog row.
DELETE FROM public.feature_flag WHERE feature_id = 'MEMORY_MODULE';
DELETE FROM public.feature WHERE value = 'MEMORY_MODULE';
