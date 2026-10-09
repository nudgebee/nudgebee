-- Tidy the "AI investigation & events" flags in tenant settings.
--
-- 1. EVENT_DEBUG_ANALYSIS_DISABLED ("Deep event analysis") is folded into
--    EVENT_AUTO_AI_SUMMARY ("Automatic event analysis"). Turned off, it already
--    stopped all analysis, so for new events it duplicated the automatic switch.
--    A scope that had it off (stored 'enabled', the inverted flag) gets
--    automatic analysis turned off at the same scope, so those accounts keep
--    getting no analysis on arrival. Events opened on demand are analysed again.
-- 2. GENERATE_RCA is removed. Only the investigate page read it, to show the
--    "Generate RCA" button; the button now shows for anyone with write access.
--
-- Child rows go first: feature_flag, billing_plan_features and
-- billing_feature_mapping reference feature.value with RESTRICT/NO ACTION, so
-- deleting a feature row first would fail and take the migration Job down.
-- TROUBLESHOOT is only hidden in the UI; it anchors the billing 'incidents' dimension.

UPDATE public.feature_flag a
   SET status = 'disabled'
  FROM public.feature_flag d
 WHERE d.feature_id = 'EVENT_DEBUG_ANALYSIS_DISABLED' AND d.status = 'enabled'
   AND a.feature_id = 'EVENT_AUTO_AI_SUMMARY'
   AND a.tenant_id = d.tenant_id
   AND a.account_id IS NOT DISTINCT FROM d.account_id;

INSERT INTO public.feature_flag (feature_id, tenant_id, account_id, status)
SELECT 'EVENT_AUTO_AI_SUMMARY', d.tenant_id, d.account_id, 'disabled'
  FROM public.feature_flag d
 WHERE d.feature_id = 'EVENT_DEBUG_ANALYSIS_DISABLED' AND d.status = 'enabled'
   AND NOT EXISTS (
       SELECT 1 FROM public.feature_flag a
        WHERE a.feature_id = 'EVENT_AUTO_AI_SUMMARY'
          AND a.tenant_id = d.tenant_id
          AND a.account_id IS NOT DISTINCT FROM d.account_id);

DELETE FROM public.feature_flag WHERE feature_id IN ('EVENT_DEBUG_ANALYSIS_DISABLED', 'GENERATE_RCA');

DELETE FROM public.billing_plan_features WHERE feature_id IN ('EVENT_DEBUG_ANALYSIS_DISABLED', 'GENERATE_RCA');

DELETE FROM public.billing_feature_mapping WHERE feature_id IN ('EVENT_DEBUG_ANALYSIS_DISABLED', 'GENERATE_RCA');

DELETE FROM public.feature WHERE value IN ('EVENT_DEBUG_ANALYSIS_DISABLED', 'GENERATE_RCA');
