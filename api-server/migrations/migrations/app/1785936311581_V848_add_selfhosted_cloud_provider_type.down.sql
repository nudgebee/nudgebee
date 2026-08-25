-- Only remove the lookup value when nothing references it. The foreign key is
-- ON DELETE RESTRICT, so a blind DELETE would fail once any self-hosted account
-- exists — and taking accounts down with it would be worse.
DELETE FROM cloud_provider_type
WHERE value = 'SelfHosted'
  AND NOT EXISTS (SELECT 1 FROM cloud_accounts WHERE cloud_provider = 'SelfHosted');
