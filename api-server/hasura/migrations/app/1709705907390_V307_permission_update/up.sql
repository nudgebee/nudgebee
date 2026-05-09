
alter table "public"."jira_configurations" add constraint "jira_configurations_tenant_name_key" unique ("tenant", "name");

alter table "public"."slack_channels" add constraint "slack_channels_tenant_id_installation_id_key" unique ("tenant_id", "installation_id");

alter table "public"."ms_teams_channels" add constraint "ms_teams_channels_tenant_id_installation_id_key" unique ("tenant_id", "installation_id");
