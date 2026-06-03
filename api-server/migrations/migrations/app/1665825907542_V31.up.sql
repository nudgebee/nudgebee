
create or replace view spends_amount_sum_daily_aggregate as
    select s.tenant
        , s."date"::date created_at_date
        , sum(s.amount) as amount
        , s.business_unit
        , pa.project_id
        , pa.account_id
    from spends s
    join project_accounts pa on pa.account_id = s.cloud_account
    group by s.tenant, s.business_unit, pa.project_id, pa.account_id, s."date"::date
    order by s."date"::date;
