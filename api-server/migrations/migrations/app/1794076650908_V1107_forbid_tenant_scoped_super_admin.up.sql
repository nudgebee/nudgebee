-- Super admin is platform-wide, so it is valid only on a user_roles row with
-- tenant_id IS NULL. Role sync no longer writes it onto a tenant-scoped row and
-- the code ignores such rows; this removes the ones that exist and forbids new
-- ones: tenant-scoped rows in user_roles, and any row in group_roles (a group
-- always belongs to one tenant). The role list matches security.IsSuperAdminRole
-- and app/src/lib/superAdminRoles.ts.
--
-- Order: the CHECK constraints go on NOT VALID first. That takes ACCESS EXCLUSIVE
-- on both tables until commit, so the pods still serving during an upgrade cannot
-- write a violating row between the DELETE and the VALIDATE. Each removed grant
-- is recorded in `audit` (category ROLES, actor API_SERVICE, transaction_id
-- 'V1107', event_state = the removed row) before it is deleted.
--
-- Lock window: ACCESS EXCLUSIVE on user_roles and group_roles for the whole file.
-- Both hold one row per grant; expect well under a second. Requests that build a
-- security context wait for it.
--
-- The down migration drops the constraints but cannot bring the rows back. A
-- legitimate super admin is re-granted as a user_roles row with tenant_id IS NULL.

ALTER TABLE user_roles DROP CONSTRAINT IF EXISTS user_roles_super_admin_not_tenant_scoped;
ALTER TABLE user_roles ADD CONSTRAINT user_roles_super_admin_not_tenant_scoped
    CHECK (role NOT IN ('super_admin', 'super_admin_readonly') OR tenant_id IS NULL) NOT VALID;

ALTER TABLE group_roles DROP CONSTRAINT IF EXISTS group_roles_no_super_admin;
ALTER TABLE group_roles ADD CONSTRAINT group_roles_no_super_admin
    CHECK (role NOT IN ('super_admin', 'super_admin_readonly')) NOT VALID;

INSERT INTO audit (user_id, tenant_id, event_category, event_type, event_prev_state, event_state,
                   event_actor, event_target, event_action, event_status, transaction_id, event_attr)
SELECT NULL, ur.tenant_id::text, 'ROLES', 'ROLE_USER_DELETE', row_to_json(ur)::text, row_to_json(ur)::text,
       'API_SERVICE', ur.user_id::text, 'DELETE', 'SUCCESS', 'V1107',
       '{"table":"user_roles","op":"DELETE","reason":"tenant-scoped super-admin grant removed by V1107"}'
FROM user_roles ur
WHERE ur.role IN ('super_admin', 'super_admin_readonly') AND ur.tenant_id IS NOT NULL;

INSERT INTO audit (user_id, tenant_id, event_category, event_type, event_prev_state, event_state,
                   event_actor, event_target, event_action, event_status, transaction_id, event_attr)
SELECT NULL, ug.tenant::text, 'ROLES', 'ROLE_GROUP_DELETE', row_to_json(gr)::text, row_to_json(gr)::text,
       'API_SERVICE', gr.group_id::text, 'DELETE', 'SUCCESS', 'V1107',
       '{"table":"group_roles","op":"DELETE","reason":"group super-admin grant removed by V1107"}'
FROM group_roles gr
JOIN user_groups ug ON ug.id = gr.group_id
WHERE gr.role IN ('super_admin', 'super_admin_readonly');

DELETE FROM user_roles WHERE role IN ('super_admin', 'super_admin_readonly') AND tenant_id IS NOT NULL;
DELETE FROM group_roles WHERE role IN ('super_admin', 'super_admin_readonly');

ALTER TABLE user_roles VALIDATE CONSTRAINT user_roles_super_admin_not_tenant_scoped;
ALTER TABLE group_roles VALIDATE CONSTRAINT group_roles_no_super_admin;
