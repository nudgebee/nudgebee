-- audit.event_prev_state is NOT NULL, so a real SQL NULL prev-state (the
-- correct value on INSERT, which has no "before" state) fails the
-- INSERT INTO audit and is swallowed by this trigger's own
-- `EXCEPTION WHEN OTHERS`. Use the JSON literal 'null' instead — a real,
-- non-NULL value — matching what the Go-side CreateAudit path already
-- stores for a nil EventPrevState (json.Marshal(nil) -> "null").
--
-- fn_audit_redact_is_sensitive_key / fn_audit_redact_jsonb port redact.go's
-- key-based redaction (isSensitiveKey/redactGeneric) into this trigger, so
-- credential-valued columns (e.g. a stored integration password or bot
-- token) are never persisted in cleartext here, the same guarantee the
-- Go-side CreateAudit path enforces via redactAudit(). Value-content
-- scrubbing (AWS key / PEM patterns) is not ported — every table behind
-- this trigger is structured columns, not free text, so key-based
-- redaction alone is sufficient.
CREATE OR REPLACE FUNCTION fn_audit_redact_is_sensitive_key(key TEXT)
RETURNS BOOLEAN AS $$
DECLARE
    normalized TEXT := regexp_replace(lower(key), '[^a-z0-9]', '', 'g');
BEGIN
    IF normalized = '' THEN
        RETURN FALSE;
    END IF;
    IF normalized ~ '(secret|password|passwd|passphrase|apikey|accesskey|privatekey|signingkey|encryptionkey)' THEN
        RETURN TRUE;
    END IF;
    -- Sensitive only as the field's final segment, so this matches token /
    -- refresh_token / access_token_v2 while leaving token_count,
    -- cookie_policy, credential_type alone. Nested payloads from third-party
    -- integrations (Slack, Teams, Jira) commonly use camelCase or kebab-case
    -- keys, so both boundary styles are checked.
    RETURN lower(key) ~ '(^|[^a-z0-9])(token|authorization|cookie|bearer|credential|credentials)(_v?[0-9]+)*$'
        OR key ~ '[a-z](Token|Authorization|Cookie|Bearer|Credential|Credentials)(_v?[0-9]+)*$';
END;
$$ LANGUAGE plpgsql IMMUTABLE;

CREATE OR REPLACE FUNCTION fn_audit_redact_jsonb(val JSONB)
RETURNS JSONB AS $$
DECLARE
    result JSONB;
BEGIN
    IF val IS NULL THEN
        RETURN NULL;
    END IF;

    IF jsonb_typeof(val) = 'object' THEN
        SELECT COALESCE(
            jsonb_object_agg(
                key,
                CASE WHEN fn_audit_redact_is_sensitive_key(key) THEN '"[REDACTED]"'::jsonb
                     ELSE fn_audit_redact_jsonb(value) END
            ),
            '{}'::jsonb
        ) INTO result FROM jsonb_each(val);
        RETURN result;
    ELSIF jsonb_typeof(val) = 'array' THEN
        SELECT COALESCE(jsonb_agg(fn_audit_redact_jsonb(value)), '[]'::jsonb)
        INTO result FROM jsonb_array_elements(val);
        RETURN result;
    ELSE
        RETURN val;
    END IF;
END;
$$ LANGUAGE plpgsql IMMUTABLE;

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
        v_prev_state := 'null'::jsonb;
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
    -- (Hasura/JSON default) collapses to NULL. event_state/event_prev_state
    -- are redacted immediately before the write.
    INSERT INTO audit (
        user_id, tenant_id, account_id, event_time,
        event_category, event_type, event_prev_state, event_state,
        event_actor, event_target, event_action, event_status, event_attr
    ) VALUES (
        NULLIF(v_user_id, '')::uuid, COALESCE(v_tenant_id, ''), COALESCE(v_account_id, ''), NOW(),
        v_event_category, v_event_type,
        fn_audit_redact_jsonb(v_prev_state)::text, fn_audit_redact_jsonb(v_event_state)::text,
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
