-- Marks which LLM integration an account resolves to when more than one is
-- enabled. Follows the existing default_log_provider / default_traces_provider /
-- default_metrics_provider convention on this table: the flag is a property of
-- the (integration, account) link, not of the integration, because one
-- integration can be linked to several accounts.
ALTER TABLE integrations_cloud_accounts
    ADD COLUMN IF NOT EXISTS default_llm_provider boolean NOT NULL DEFAULT false;

-- Backfill every account that currently has exactly one enabled LLM
-- integration, so its resolution stays deterministic the moment a second
-- config is added. Without this, adding a second config to an existing account
-- would leave zero flagged defaults, the resolver would find the choice
-- ambiguous, and the account would silently fall back to the ENV credential.
UPDATE integrations_cloud_accounts ia
   SET default_llm_provider = true
  FROM integrations i
 WHERE i.id = ia.integration_id
   AND i."type" = 'llm'
   AND i.status = 'enabled'
   AND (
       SELECT count(*)
         FROM integrations_cloud_accounts ia2
         JOIN integrations i2 ON i2.id = ia2.integration_id
        WHERE ia2.cloud_account_id = ia.cloud_account_id
          AND i2."type" = 'llm'
          AND i2.status = 'enabled'
   ) = 1;
