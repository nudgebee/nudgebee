
alter table "public"."ms_teams_channels" drop constraint "ms_teams_channels_tenant_id_installation_id_key";

alter table "public"."slack_channels" drop constraint "slack_channels_tenant_id_installation_id_key";

alter table "public"."jira_configurations" drop constraint "jira_configurations_tenant_name_key";
