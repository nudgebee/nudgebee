-- Drops the V1107 constraints. The grants the up migration deleted are not
-- restored: they are recorded in `audit` (transaction_id 'V1107'), and a
-- legitimate super admin is re-granted as a user_roles row with tenant_id IS NULL.
ALTER TABLE group_roles DROP CONSTRAINT IF EXISTS group_roles_no_super_admin;
ALTER TABLE user_roles DROP CONSTRAINT IF EXISTS user_roles_super_admin_not_tenant_scoped;
