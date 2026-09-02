-- cloud_accounts.cloud_provider is a foreign key to cloud_provider_type, so a
-- self-hosted VM fleet cannot be onboarded until the value exists here — the
-- insert fails with cloud_accounts_cloud_provider_fkey.
--
-- Unlike account_type, which is free text, this column is constrained; that is
-- why the 'SelfHosted' provider needs a migration and its companion 'vm'
-- account type does not.
INSERT INTO cloud_provider_type (value, comment)
VALUES ('SelfHosted', 'Self-hosted VM fleet reached through an agent, with no cloud provider behind it')
ON CONFLICT (value) DO NOTHING;
