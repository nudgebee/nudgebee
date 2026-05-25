INSERT INTO account_env_type (value)
SELECT 'non_prod'
WHERE NOT EXISTS (
    SELECT 1 FROM account_env_type WHERE value = 'non_prod'
);


UPDATE cloud_accounts SET account_env = 'non_prod' WHERE account_env = 'non-prod';

alter table "public"."cloud_accounts" alter column "account_env" set default 'non_prod'::text;

DELETE FROM account_env_type WHERE value = 'non-prod';
