-- #41036: a user is a person or a bee (an AI worker that acts under its own
-- account). A bee never logs in, gets no invitation and is left out of people
-- lists; custom roles, run principals and audit treat it like any other user.
-- Constant default: a catalogue-only change on PG 11+, no table rewrite.
ALTER TABLE users ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'person';

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'users_kind_check') THEN
        ALTER TABLE users ADD CONSTRAINT users_kind_check CHECK (kind IN ('person', 'bee'));
    END IF;
END $$;

-- A bee gets its rights only from custom roles. Built-in roles (user_roles) and
-- user groups (which carry built-in roles, notifications and approver lists)
-- are refused here, for every write path, present and future.
CREATE OR REPLACE FUNCTION refuse_bee_member() RETURNS trigger AS $$
DECLARE
    member uuid;
BEGIN
    IF TG_TABLE_NAME = 'user_roles' THEN
        member := NEW.user_id;
    ELSE
        member := NEW."user";
    END IF;
    IF EXISTS (SELECT 1 FROM users WHERE id = member AND kind = 'bee') THEN
        RAISE EXCEPTION 'a bee account cannot hold built-in roles or join user groups; give it a custom role'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS user_roles_refuse_bee ON user_roles;
CREATE TRIGGER user_roles_refuse_bee BEFORE INSERT OR UPDATE OF user_id ON user_roles
    FOR EACH ROW EXECUTE FUNCTION refuse_bee_member();

DROP TRIGGER IF EXISTS usergroup_users_refuse_bee ON usergroup_users;
CREATE TRIGGER usergroup_users_refuse_bee BEFORE INSERT OR UPDATE OF "user" ON usergroup_users
    FOR EACH ROW EXECUTE FUNCTION refuse_bee_member();

-- A user's kind never changes: a person turned bee would keep roles and groups,
-- and a bee turned person could sign in.
CREATE OR REPLACE FUNCTION refuse_user_kind_change() RETURNS trigger AS $$
BEGIN
    IF NEW.kind IS DISTINCT FROM OLD.kind THEN
        RAISE EXCEPTION 'a user''s kind cannot change' USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS users_kind_immutable ON users;
CREATE TRIGGER users_kind_immutable BEFORE UPDATE OF kind ON users
    FOR EACH ROW EXECUTE FUNCTION refuse_user_kind_change();
