-- cloud_account_usage_report (~3.1M rows, high insert/delete churn).
-- Serves both DELETE paths:
--   * monthly rollover  : WHERE account_id=? AND report_date >= ? AND report_date < ?
--     (rewritten from TO_CHAR(report_date,'yyyy-mm')=? which could not use an index)
--   * 90-day retention  : WHERE report_date < now() - interval '90 days'
--
-- OPERATOR NOTE: on a table this large, create the index out-of-band BEFORE deploy to
-- avoid a long write lock during the migration:
--     CREATE INDEX CONCURRENTLY idx_caur_account_report_date
--         ON cloud_account_usage_report (account_id, report_date);
-- This migration is then a no-op (IF NOT EXISTS). golang-migrate wraps migrations in a
-- transaction, so CONCURRENTLY cannot run here.
CREATE INDEX IF NOT EXISTS idx_caur_account_report_date
    ON cloud_account_usage_report (account_id, report_date);
