CREATE
OR REPLACE VIEW "public"."spends_account_aggregate" AS
SELECT
  s.cloud_account_id,
  sum(amount) AS count
FROM
  spends s
GROUP BY
  s.cloud_account_id;
