-- Adds an explicit, per-model vision/image-input capability flag to the
-- existing model catalog table (keyed by model_name + provider_name, same
-- place pricing already lives). Replaces guesswork with recorded fact.
--
-- llm-server's IsVisionCapableModel() previously decided whether a model
-- accepts image attachments purely via a hardcoded regex deny-list — any
-- model not matching one of ~6 known patterns was just assumed to support
-- images. That guess can be wrong for newer or custom-configured models.
--
-- NULL means "not recorded yet" (distinct from false) — llm-server falls
-- back to the regex heuristic for any row where this column is NULL, so
-- adding the column is behavior-neutral until rows are explicitly set.
--
-- Backfill below is a mechanical translation of the current deny-list
-- (llm/llm-server/agents/core/image_utils.go defaultNonVisionPatterns) for
-- any pricing rows that already exist for those (deprecated) models. It is
-- a no-op today (none of those models currently have pricing rows) but is
-- included so this migration is the single source of truth for the
-- known-non-vision set going forward.

ALTER TABLE public.llm_model_pricing
  ADD COLUMN supports_image_input boolean NULL;

COMMENT ON COLUMN public.llm_model_pricing.supports_image_input IS
  'Explicit vision/image-input capability verdict for this model. NULL = not recorded yet (caller falls back to its own default heuristic).';

UPDATE public.llm_model_pricing
SET supports_image_input = false
WHERE model_name ~* '(^|[^a-z0-9])gpt-3\.5([^a-z0-9]|$)'
   OR model_name ~* '(^|[^a-z0-9])gpt-4-base([^a-z0-9]|$)'
   OR model_name ~* '(^|[^a-z0-9])claude-2([^a-z]|$)'
   OR model_name ~* '(^|[^a-z0-9])claude-instant([^a-z0-9]|$)'
   OR model_name ~* '(^|[^a-z0-9])titan-text([^a-z0-9]|$)'
   OR model_name ~* '([.\-/]|^)cohere\.command-(text|light)([^a-z0-9]|$)';
