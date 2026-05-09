INSERT INTO roles (value, display_name) VALUES ('super_admin', 'Super Admin')
ON CONFLICT (value) DO NOTHING;

INSERT INTO roles (value, display_name) VALUES ('super_admin_readonly', 'Super Admin ReadOnly')
ON CONFLICT (value) DO NOTHING;
