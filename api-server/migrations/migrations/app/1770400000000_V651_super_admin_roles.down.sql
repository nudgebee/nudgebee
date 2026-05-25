DELETE FROM user_roles WHERE role IN ('super_admin', 'super_admin_readonly');
DELETE FROM roles WHERE value IN ('super_admin', 'super_admin_readonly');
