DROP TRIGGER IF EXISTS users_kind_immutable ON users;
DROP FUNCTION IF EXISTS refuse_user_kind_change();
DROP TRIGGER IF EXISTS usergroup_users_refuse_bee ON usergroup_users;
DROP TRIGGER IF EXISTS user_roles_refuse_bee ON user_roles;
DROP FUNCTION IF EXISTS refuse_bee_member();
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_kind_check;
ALTER TABLE users DROP COLUMN IF EXISTS kind;
