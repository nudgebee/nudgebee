-- Add PR lifecycle tracking columns to event_resolution
ALTER TABLE public.event_resolution
  ADD COLUMN IF NOT EXISTS pr_iteration_count INTEGER DEFAULT 0,
  ADD COLUMN IF NOT EXISTS pr_lifecycle_state TEXT DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS last_pr_check_at TIMESTAMP;

-- Add PR lifecycle tracking columns to recommendation_resolution
ALTER TABLE public.recommendation_resolution
  ADD COLUMN IF NOT EXISTS pr_iteration_count INTEGER DEFAULT 0,
  ADD COLUMN IF NOT EXISTS pr_lifecycle_state TEXT DEFAULT NULL,
  ADD COLUMN IF NOT EXISTS last_pr_check_at TIMESTAMP;

-- Fix resolver_type CHECK constraint to allow 'NBLLM' (code agent uses this value)
ALTER TABLE public.event_resolution DROP CONSTRAINT IF EXISTS resolver_type_check;
ALTER TABLE public.event_resolution ADD CONSTRAINT resolver_type_check
  CHECK (resolver_type = ANY (ARRAY['User'::text, 'AutoOptimize'::text, 'AutoRunbook'::text, 'NBLLM'::text]));

ALTER TABLE public.recommendation_resolution DROP CONSTRAINT IF EXISTS resolver_type_check;
ALTER TABLE public.recommendation_resolution ADD CONSTRAINT resolver_type_check
  CHECK (resolver_type = ANY (ARRAY['User'::text, 'AutoOptimize'::text, 'AutoRunbook'::text, 'NBLLM'::text]));
