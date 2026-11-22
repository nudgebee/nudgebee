-- Records which default_*_provider columns on a link the user set from the
-- Integrations UI, so the k8s collector's heartbeat leaves them alone instead of
-- handing the default back to the agent's own provider. Holds column names, e.g.
-- '{default_log_provider}'. Written by api-server's integration save path, read
-- by the collector and by the default-provider lookup.
--
-- A constant default makes this a catalog-only change on Postgres 11+: no table
-- rewrite, and the ACCESS EXCLUSIVE lock is held only for the catalog update.
-- No backfill: which existing defaults a user chose was never recorded.
ALTER TABLE integrations_cloud_accounts
    ADD COLUMN IF NOT EXISTS user_set_defaults text[] NOT NULL DEFAULT '{}';
