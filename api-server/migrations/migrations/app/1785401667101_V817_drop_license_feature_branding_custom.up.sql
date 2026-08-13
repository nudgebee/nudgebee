-- Retire the `branding.custom` license feature.
--
-- Custom tenant branding no longer consults the license or feature_flag at all:
-- app/src/ee/branding/serverInit.ts registers its provider unconditionally and
-- resolves from TENANT_BRANDING_FILE plus the mounted theme file. Nothing in the
-- frontend or in services-server reads `branding.custom`, so both the catalog
-- entry and any rows the license bridge wrote for it are dead weight.
--
-- ORDER OF OPERATIONS: re-mint the license JWT WITHOUT `branding.custom` in its
-- `features` claim and roll services-server BEFORE this migration lands. The
-- bridge (ee/license/bridge.go, 10-minute reconcile) re-upserts every feature the
-- license still carries; with the catalog row gone that upsert fails the
-- feature_flag.feature_id -> feature.value FK with 23503 on every tick.
--
-- feature_flag.feature_id -> feature.value is ON DELETE restrict, so the flag
-- rows must go first.
DELETE FROM feature_flag WHERE feature_id = 'branding.custom';
DELETE FROM feature WHERE value = 'branding.custom';
