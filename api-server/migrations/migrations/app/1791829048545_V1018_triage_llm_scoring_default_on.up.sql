-- AI alert prioritisation (TRIAGE_LLM_SCORING) becomes on by default for tenants created from now
-- on. Every tenant that exists today keeps the behaviour it has: those that switched it on keep their
-- 'enabled' row, and every other tenant gets an explicit 'disabled' row, so flipping the default
-- changes nothing for them. Existing tenants are moved over one at a time from Tenant Settings.
--
-- The 'disabled' rows below are the ones this migration added (created_at = when it ran); an
-- admin's own "off" is indistinguishable otherwise.
--
-- The scorer reads only the tenant-level row (account_id IS NULL), so every tenant needs one, even
-- a tenant whose only row is account-level. Where the UNIQUE (tenant_id, feature_id) constraint
-- from V545 is in place, such a tenant has room for one row only: the INSERT skips it
-- (ON CONFLICT DO NOTHING) and the UPDATE turns that account-level row into the tenant-level
-- 'disabled' row. The account-level row never affected scoring, so the tenant stays off either way.
--
-- Lock window: an INSERT ... SELECT over the tenant table and two small UPDATEs; all short.

INSERT INTO public.feature_flag (feature_id, tenant_id, status)
SELECT 'TRIAGE_LLM_SCORING', t.id, 'disabled'
  FROM public.tenant t
 WHERE NOT EXISTS (
       SELECT 1 FROM public.feature_flag f
        WHERE f.feature_id = 'TRIAGE_LLM_SCORING'
          AND f.tenant_id = t.id
          AND f.account_id IS NULL)
ON CONFLICT DO NOTHING;

UPDATE public.feature_flag f
   SET account_id = NULL, status = 'disabled'
 WHERE f.feature_id = 'TRIAGE_LLM_SCORING'
   AND f.account_id IS NOT NULL
   AND NOT EXISTS (
       SELECT 1 FROM public.feature_flag g
        WHERE g.feature_id = f.feature_id
          AND g.tenant_id = f.tenant_id
          AND g.account_id IS NULL);

UPDATE public.feature
   SET polarity = 'default_on'
 WHERE value = 'TRIAGE_LLM_SCORING';
