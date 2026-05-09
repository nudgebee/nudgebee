
alter table "public"."jira_configurations" add column "is_active" bool
 not null default 'true';


UPDATE tickets SET severity = 'NA' where severity is null and platform = 'github';
