
alter table "public"."integrations" drop constraint "integrations_updated_by_fkey";

alter table "public"."integration_config_values" drop constraint "integration_config_values_name_value_type_key";
