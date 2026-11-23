-- Remove all per-tenant enrolments first (FK constraint), then the catalog row.
DELETE FROM public.feature_flag WHERE feature_id = 'EVENT_REMEDIATION_LOOP';
DELETE FROM public.feature WHERE value = 'EVENT_REMEDIATION_LOOP';
