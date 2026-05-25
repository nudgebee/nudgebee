
alter table "public"."integration_config_values" add constraint "integration_config_values_name_value_type_key" unique ("name", "value", "type");

alter table "public"."integrations"
  add constraint "integrations_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;
