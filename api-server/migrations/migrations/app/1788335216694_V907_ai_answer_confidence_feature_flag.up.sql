-- Registers the post-hoc AI answer-confidence grader in the feature catalog.
-- Registering does NOT enable it: with no public.feature_flag row the gate returns
-- false and nothing is graded. Opt-in because grading costs one extra LLM call per
-- investigation turn. Enable per tenant from Settings > Tenant Settings > Feature
-- Flags, or: INSERT INTO public.feature_flag (feature_id, tenant_id, status)
--            VALUES ('AI_ANSWER_CONFIDENCE', '<tenant-uuid>', 'enabled');
--
-- display_name/category/polarity are the metadata V900 added; a row without them
-- renders in Tenant Settings under its raw key. polarity='opt_in' matches the
-- reader (common.IsFeatureEnabled answers false with no row).
INSERT INTO public.feature (value, display_name, category, polarity, description)
VALUES (
    'AI_ANSWER_CONFIDENCE',
    'Answer confidence on investigations',
    'assistant',
    'opt_in',
    'Rates how well each investigation answer is backed by the tools that actually ran, and shows the result as a high/medium/low badge on the answer. Costs one extra AI call per investigation. Off means answers are unchanged, with no badge.'
)
ON CONFLICT (value) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    category     = EXCLUDED.category,
    polarity     = EXCLUDED.polarity,
    description  = EXCLUDED.description;
