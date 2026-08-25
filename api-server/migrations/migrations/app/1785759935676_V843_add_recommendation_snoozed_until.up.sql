-- Snooze support for recommendations. A snoozed recommendation is stored as
-- status='Dismissed' with snoozed_until set, so every existing consumer that
-- filters on Open (nudges, digest, finops score, AutoOptimize selectors, the
-- optimise default list) suppresses it with no query changes; the expiry sweep
-- returns it to Open when the timestamp passes. A plain dismissal leaves
-- snoozed_until NULL.

ALTER TABLE public.recommendation
    ADD COLUMN IF NOT EXISTS snoozed_until timestamp;
