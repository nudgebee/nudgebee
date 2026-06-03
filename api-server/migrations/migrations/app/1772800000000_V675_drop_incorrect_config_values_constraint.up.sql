
-- Drop the incorrect unique constraint on (name, value, type) which prevents
-- multiple integrations from having config values with the same name/value/type.
alter table "public"."integration_config_values" drop constraint if exists "integration_config_values_name_value_type_key";

-- Ensure the correct unique constraint on (integration_id, name) exists.
-- This allows each integration to have one value per config name.
create unique index if not exists integration_config_values_config_config_name on integration_config_values using btree (integration_id, name);
