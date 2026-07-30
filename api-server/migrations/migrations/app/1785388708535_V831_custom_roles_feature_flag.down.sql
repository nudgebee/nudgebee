-- Remove all per-tenant enrolments first (FK constraint), then the catalog row.
DELETE FROM public.feature_flag WHERE feature_id = 'CUSTOM_ROLES';
DELETE FROM public.feature WHERE value = 'CUSTOM_ROLES';
