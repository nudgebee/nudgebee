

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- drop function compliance_check_findings_aggregate;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE OR REPLACE VIEW compliance_check_findings_count_aggregate AS
--     select tenant, "created_at"::date as created_at_date, count(*)
--     from compliance_check_findings ccf
--     group by tenant, "created_at"::date
--     order by "created_at"::date asc;

-- Could not auto-generate a down migration.
-- Please write an appropriate down migration for the SQL below:
-- CREATE OR REPLACE FUNCTION compliance_check_findings_aggregate(hasura_session json)
-- RETURNS TABLE(tenant uuid, created_at_date date, count int) AS
-- $$
--     select tenant, "created_at"::date as created_at_date, count(*)
--     from compliance_check_findings ccf
--     where  ccf.tenant = (hasura_session ->> 'x-hasura-user-id')::uuid
--     group by tenant, "created_at"::date
--     order by "created_at"::date asc
-- $$
-- LANGUAGE SQL;
