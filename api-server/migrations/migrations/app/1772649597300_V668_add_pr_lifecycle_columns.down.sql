-- Remove PR lifecycle tracking columns from event_resolution
ALTER TABLE public.event_resolution
  DROP COLUMN IF EXISTS pr_iteration_count,
  DROP COLUMN IF EXISTS pr_lifecycle_state,
  DROP COLUMN IF EXISTS last_pr_check_at;

-- Remove PR lifecycle tracking columns from recommendation_resolution
ALTER TABLE public.recommendation_resolution
  DROP COLUMN IF EXISTS pr_iteration_count,
  DROP COLUMN IF EXISTS pr_lifecycle_state,
  DROP COLUMN IF EXISTS last_pr_check_at;

-- Revert resolver_type CHECK constraint
ALTER TABLE public.event_resolution DROP CONSTRAINT IF EXISTS resolver_type_check;
ALTER TABLE public.event_resolution ADD CONSTRAINT resolver_type_check
  CHECK (resolver_type = ANY (ARRAY['User'::text, 'AutoOptimize'::text, 'AutoRunbook'::text]));

ALTER TABLE public.recommendation_resolution DROP CONSTRAINT IF EXISTS resolver_type_check;
ALTER TABLE public.recommendation_resolution ADD CONSTRAINT resolver_type_check
  CHECK (resolver_type = ANY (ARRAY['User'::text, 'AutoOptimize'::text, 'AutoRunbook'::text]));
