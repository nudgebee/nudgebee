CREATE INDEX IF NOT EXISTS idx_user_roles_tenant_user
    ON user_roles (tenant_id, user_id);
