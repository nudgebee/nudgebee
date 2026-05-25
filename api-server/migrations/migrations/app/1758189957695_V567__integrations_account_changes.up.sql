
alter table "public"."integrations" drop constraint "integrations_accountid_fkey";

DELETE from integrations where source = 'agent';

alter table "public"."integrations" add constraint "integrations_source_type_name_tenant_id_key" unique ("source", "type", "name", "tenant_id");
