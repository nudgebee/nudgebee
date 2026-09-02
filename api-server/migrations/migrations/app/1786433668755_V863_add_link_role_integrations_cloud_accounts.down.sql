-- The old 3-column constraint was never dropped by the up migration (see its
-- comment), so there's nothing to restore here — just undo what up.sql added.
alter table "public"."integrations_cloud_accounts" drop constraint if exists "integrations_cloud_accounts_link_role_unique_key";

alter table "public"."integrations_cloud_accounts" drop column if exists "link_role";
