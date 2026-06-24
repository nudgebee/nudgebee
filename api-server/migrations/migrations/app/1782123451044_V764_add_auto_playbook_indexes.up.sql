-- auto_playbook_executions: serves the billing usage aggregate
--   (services/ee/billing/service.go) WHERE status=? AND tenant_id=? AND created_at BETWEEN ? AND ?
--   GROUP BY account_id. INCLUDE(account_id) makes the aggregate index-only.
CREATE INDEX IF NOT EXISTS idx_ape_tenant_status_created
    ON auto_playbook_executions (tenant_id, status, created_at) INCLUDE (account_id);

-- auto_playbook_task: account_id backs the account-deletion cleanup DELETE
--   (services/account/service.go); execution_id / auto_playbook_id are FK columns
--   with no index, so every parent delete currently seq-scans this child table.
CREATE INDEX IF NOT EXISTS idx_apt_account_id
    ON auto_playbook_task (account_id);
CREATE INDEX IF NOT EXISTS idx_apt_execution_id
    ON auto_playbook_task (execution_id);
CREATE INDEX IF NOT EXISTS idx_apt_auto_playbook_id
    ON auto_playbook_task (auto_playbook_id);
