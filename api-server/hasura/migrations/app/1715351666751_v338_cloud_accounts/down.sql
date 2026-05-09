ALTER TABLE agent drop constraint if exists agent_tenant_account_type;
ALTER TABLE cloud_resourses drop constraint if exists cloud_resourses_account_resourse_service_type_region_key;
drop table if not exists cloud_account_usage_report;
