
alter table "public"."alert_rules" add column "last_evaluated_at" timestamp
 null;

CREATE
OR REPLACE VIEW "public"."alert_spends_view" AS
SELECT
    s.date as timestamp,
    s.tenant as tenant_id,
    s.business_unit as business_unit_id,
    s.cloud_account as account_id,
    s.cloud_resource_id as resource_id,
    s.amount as amount,
    cr.service_name as resource_service_name,
    cr.name as resource_name,
    cr.type as resource_type,
    cr.tags as resource_tags,
    cr.meta as resource_meta
FROM
  (
    spends s
    left join cloud_resourses cr on s.cloud_resource_id = cr.id
  );
