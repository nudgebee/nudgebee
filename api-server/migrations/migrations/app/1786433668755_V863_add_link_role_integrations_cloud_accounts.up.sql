alter table "public"."integrations_cloud_accounts" add column if not exists "link_role" text not null default 'own';

-- The old 3-column constraint (integration_id, cloud_account_id, tenant_id)
-- is intentionally left in place rather than dropped here: relay-server,
-- api-server, and this migration job deploy via three independent GitHub
-- Actions pipelines with no cross-workflow ordering guarantee, so old
-- relay-server code (ON CONFLICT targeting the 3-column constraint) can
-- still be running after this migration applies. Both constraints coexist
-- safely — they key on different column sets and a 'discovery_target' row's
-- cloud_account_id normally differs from its integration's 'own' row, so
-- the old constraint never blocks the new link_role-aware insert. Drop the
-- old constraint in a follow-up migration once relay-server/api-server are
-- confirmed rolled out on the new one.
--
-- Name kept short deliberately: Postgres truncates identifiers over 63
-- bytes (NAMEDATALEN), and the "obvious" longer name
-- (..._integration_id_cloud_account_id_tenant_id_role_key) truncates to the
-- exact same 63-byte prefix as the old constraint's name, causing a
-- collision at apply time — caught by the migrations validate job's
-- fresh-DB smoke test.
alter table "public"."integrations_cloud_accounts" add constraint "integrations_cloud_accounts_link_role_unique_key" unique ("integration_id", "cloud_account_id", "tenant_id", "link_role");
