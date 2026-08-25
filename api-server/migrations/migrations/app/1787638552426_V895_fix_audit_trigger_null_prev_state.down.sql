-- Revert fn_audit_trigger to its pre-V895 body and drop the redaction
-- helper functions introduced alongside it.
CREATE OR REPLACE FUNCTION fn_audit_trigger()
RETURNS TRIGGER AS $$
DECLARE
    v_user_id     TEXT;
    v_tenant_id   TEXT;
    v_account_id  TEXT;
    v_target_id   TEXT;
    v_event_state JSONB;
    v_prev_state  JSONB;
    v_event_category TEXT;
    v_event_type  TEXT;
    v_event_action TEXT;
    v_row         RECORD;
BEGIN
    -- Determine action and states
    IF TG_OP = 'DELETE' THEN
        v_row := OLD;
        v_event_state := to_jsonb(OLD);
        v_prev_state := to_jsonb(OLD);
        v_event_action := 'DELETE';
    ELSIF TG_OP = 'INSERT' THEN
        v_row := NEW;
        v_event_state := to_jsonb(NEW);
        v_prev_state := NULL;
        v_event_action := 'CREATE';
    ELSE -- UPDATE
        v_row := NEW;
        v_event_state := to_jsonb(NEW);
        v_prev_state := to_jsonb(OLD);
        v_event_action := 'UPDATE';
    END IF;

    -- Extract target ID
    v_target_id := v_event_state ->> 'id';
    IF v_target_id IS NULL THEN
        RETURN COALESCE(NEW, OLD);
    END IF;

    -- Extract user_id from row data (try multiple field names)
    v_user_id := COALESCE(
        v_event_state ->> 'user',
        v_event_state ->> 'user_id',
        v_event_state ->> 'created_by'
    );

    -- Extract tenant_id
    v_tenant_id := COALESCE(
        v_event_state ->> 'tenant',
        v_event_state ->> 'tenant_id'
    );

    -- Extract account_id
    v_account_id := COALESCE(
        v_event_state ->> 'account',
        v_event_state ->> 'account_id',
        v_event_state ->> 'cloud_account_id',
        v_event_state ->> 'cloud_account'
    );

    -- Get event_category and event_type from trigger arguments
    -- TG_ARGV[0] = event_category, TG_ARGV[1..N] = INSERT_type, UPDATE_type, DELETE_type
    v_event_category := TG_ARGV[0];
    IF TG_OP = 'INSERT' THEN
        v_event_type := TG_ARGV[1];
    ELSIF TG_OP = 'UPDATE' THEN
        v_event_type := TG_ARGV[2];
    ELSIF TG_OP = 'DELETE' THEN
        v_event_type := TG_ARGV[3];
    END IF;

    IF v_event_type IS NULL THEN
        RETURN COALESCE(NEW, OLD);
    END IF;

    -- Insert audit record. user_id needs explicit cast to uuid; empty string
    -- (Hasura/JSON default) collapses to NULL.
    INSERT INTO audit (
        user_id, tenant_id, account_id, event_time,
        event_category, event_type, event_prev_state, event_state,
        event_actor, event_target, event_action, event_status, event_attr
    ) VALUES (
        NULLIF(v_user_id, '')::uuid, COALESCE(v_tenant_id, ''), COALESCE(v_account_id, ''), NOW(),
        v_event_category, v_event_type, v_prev_state::text, v_event_state::text,
        'UI_SERVICE', v_target_id, v_event_action, 'SUCCESS',
        jsonb_build_object('table', TG_TABLE_NAME, 'op', TG_OP, 'source', 'pg_trigger')::text
    );

    RETURN COALESCE(NEW, OLD);
EXCEPTION WHEN OTHERS THEN
    -- Never fail the parent operation due to audit errors
    RAISE WARNING 'audit trigger failed on %.%: %', TG_TABLE_NAME, TG_OP, SQLERRM;
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

DROP FUNCTION IF EXISTS fn_audit_redact_jsonb(JSONB);
DROP FUNCTION IF EXISTS fn_audit_redact_is_sensitive_key(TEXT);
