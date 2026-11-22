SET LOCAL lock_timeout = '5s';

DROP INDEX IF EXISTS public.uq_event_resolution_approval_key;

-- NOT VALID: rows written as Unknown or Declined stay as they are (no data is rewritten); the old list
-- applies to new writes only.
ALTER TABLE public.event_resolution DROP CONSTRAINT IF EXISTS status_check;
ALTER TABLE public.event_resolution ADD CONSTRAINT status_check
  CHECK (status = ANY (ARRAY['InProgress'::text, 'Failed'::text, 'Success'::text, 'Configuring'::text])) NOT VALID;
