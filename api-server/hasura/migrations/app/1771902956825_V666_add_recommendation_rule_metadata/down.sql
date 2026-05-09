
ALTER TRIGGER set_recommendation_rule_updated_at ON public.recommendation_rule
    RENAME TO set_recommendation_rule_metadata_updated_at;

ALTER INDEX idx_recommendation_rule_category RENAME TO idx_recommendation_rule_metadata_category;

ALTER TABLE public.recommendation_rule RENAME TO recommendation_rule_metadata;

DELETE FROM public.recommendation_rule_metadata;

DROP TRIGGER IF EXISTS set_recommendation_rule_metadata_updated_at ON public.recommendation_rule_metadata;
DROP TABLE IF EXISTS public.recommendation_rule_metadata;
