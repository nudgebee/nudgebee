
alter table compliance_check_findings
    add column created_at_date date 
        GENERATED ALWAYS AS (created_at::date) stored;
