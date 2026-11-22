-- Back to opt-in. The 'disabled' rows the up migration added are left in place: under opt-in a
-- 'disabled' row and no row both mean off, so they change nothing, and removing them could also
-- remove an admin's own choice.
UPDATE public.feature
   SET polarity = 'opt_in'
 WHERE value = 'TRIAGE_LLM_SCORING';
