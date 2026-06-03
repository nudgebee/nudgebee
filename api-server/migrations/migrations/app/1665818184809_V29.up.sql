
CREATE
OR REPLACE VIEW "public"."spends_resource_group_aggregate" AS
select
  count(*),
  resource_group,
  sum(amount) as amount,
  date,
  tenant,
  business_unit
from
  (
    select
      distinct sum(amount) as amount,
      resource_group,
      resource_name,
      DATE_TRUNC('month', date) :: date as date,
      tenant,
      business_unit
    from
      spends
    group by
      resource_name,
      resource_group,
      DATE_TRUNC('month', date) :: date,
      tenant,
      business_unit
  ) a
group by
  resource_group,
  date,
  tenant,
  business_unit
order by
  count(*),
  date desc;

CREATE
OR REPLACE VIEW "public"."spends_resource_group_aggregate" AS
select
  count(*),
  resource_group,
  sum(amount) as amount,
  date,
  tenant,
  business_unit
from
  (
    select
      distinct sum(amount) as amount,
      resource_group,
      resource_name,
      DATE_TRUNC('month', date) :: date as date,
      tenant,
      business_unit
    from
      spends
    group by
      resource_name,
      resource_group,
      DATE_TRUNC('month', date) :: date,
      tenant,
      business_unit
  ) a
group by
  resource_group,
  date,
  tenant,
  business_unit
order by
  date,count(*) desc;

CREATE
OR REPLACE VIEW "public"."spends_resource_group_aggregate" AS
select
  count(*),
  resource_group,
  sum(amount) as amount,
  date,
  tenant,
  business_unit
from
  (
    select
      distinct sum(amount) as amount,
      resource_group,
      resource_name,
      DATE_TRUNC('month', date) :: date as date,
      tenant,
      business_unit
    from
      spends
    group by
      resource_name,
      resource_group,
      DATE_TRUNC('month', date) :: date,
      tenant,
      business_unit
  ) a
group by
  resource_group,
  date,
  tenant,
  business_unit
order by
  date,count(*) desc;
