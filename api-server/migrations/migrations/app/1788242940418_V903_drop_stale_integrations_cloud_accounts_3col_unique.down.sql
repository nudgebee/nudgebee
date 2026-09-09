-- Restore the 3-column unique constraint. This fails if same-account
-- discovery_target rows were added after the up ran (their whole point) -- that
-- is the correct outcome for a down migration: the schema genuinely can't go
-- back once that data exists.
--
-- Name is the 63-byte form V569 ended up with after Postgres truncated its
-- "..._tenant_id_key" identifier; using it verbatim keeps the restore free of a
-- truncation NOTICE.
alter table "public"."integrations_cloud_accounts"
  add constraint "integrations_cloud_accounts_integration_id_cloud_account_id_ten" unique ("integration_id", "cloud_account_id", "tenant_id");
