-- Structural revert only — restores V700's original (broken) cast. Not
-- recommended: this reintroduces the silent event_history failure described
-- in the .up.sql comment.
CREATE OR REPLACE FUNCTION fn_event_history_trigger()
RETURNS TRIGGER AS $$
DECLARE
    v_history_id TEXT;
    v_change_reason TEXT;
    v_old_val TEXT;
    v_new_val TEXT;
    v_metadata JSONB;
    priority_order JSONB := '{"INFO":0,"LOW":1,"MEDIUM":2,"HIGH":3,"CRITICAL":4}'::jsonb;
BEGIN
    IF TG_OP != 'UPDATE' THEN
        RETURN NEW;
    END IF;

    IF OLD.status IS DISTINCT FROM NEW.status
       AND NEW.status = 'CLOSED'
       AND NOT (OLD.priority IS DISTINCT FROM NEW.priority) THEN
        RETURN NEW;
    END IF;

    IF OLD.priority IS DISTINCT FROM NEW.priority THEN
        v_old_val := OLD.priority;
        v_new_val := NEW.priority;

        IF (priority_order ->> NEW.priority)::int > (priority_order ->> OLD.priority)::int THEN
            v_change_reason := 'escalation';
        ELSE
            v_change_reason := 'priority_changed';
        END IF;

        v_metadata := '{}'::jsonb;
        IF NEW.aggregation_key IS NOT NULL THEN
            v_metadata := v_metadata || jsonb_build_object('aggregation_key', NEW.aggregation_key);
        END IF;
        IF NEW.source IS NOT NULL THEN
            v_metadata := v_metadata || jsonb_build_object('source', NEW.source);
        END IF;

        v_history_id := encode(sha256(
            convert_to(NEW.id || '|priority|' || COALESCE(v_old_val,'') || '|' || COALESCE(v_new_val,''), 'UTF8')
        ), 'hex');
        v_history_id := left(v_history_id, 32);

        INSERT INTO event_history (id, event_id, tenant_id, cloud_account_id, change_type, old_value, new_value, change_reason, metadata)
        VALUES (v_history_id, NEW.id, NEW.tenant, NEW.cloud_account_id, 'priority',
                to_jsonb(v_old_val), to_jsonb(v_new_val), v_change_reason,
                CASE WHEN v_metadata = '{}'::jsonb THEN NULL ELSE v_metadata END)
        ON CONFLICT (id) DO NOTHING;
    END IF;

    IF OLD.status IS DISTINCT FROM NEW.status THEN
        v_old_val := OLD.status;
        v_new_val := NEW.status;

        CASE NEW.status
            WHEN 'RESOLVED' THEN v_change_reason := 'resolution_applied';
            WHEN 'CLOSED' THEN v_change_reason := 'event_closed';
            ELSE v_change_reason := 'status_changed';
        END CASE;

        v_history_id := encode(sha256(
            convert_to(NEW.id || '|status|' || COALESCE(v_old_val,'') || '|' || COALESCE(v_new_val,''), 'UTF8')
        ), 'hex');
        v_history_id := left(v_history_id, 32);

        INSERT INTO event_history (id, event_id, tenant_id, cloud_account_id, change_type, old_value, new_value, change_reason)
        VALUES (v_history_id, NEW.id, NEW.tenant, NEW.cloud_account_id, 'status',
                to_jsonb(v_old_val), to_jsonb(v_new_val), v_change_reason)
        ON CONFLICT (id) DO NOTHING;
    END IF;

    IF OLD.urgency IS DISTINCT FROM NEW.urgency THEN
        v_old_val := OLD.urgency;
        v_new_val := NEW.urgency;

        v_history_id := encode(sha256(
            convert_to(NEW.id || '|urgency|' || COALESCE(v_old_val,'') || '|' || COALESCE(v_new_val,''), 'UTF8')
        ), 'hex');
        v_history_id := left(v_history_id, 32);

        INSERT INTO event_history (id, event_id, tenant_id, cloud_account_id, change_type, old_value, new_value, change_reason)
        VALUES (v_history_id, NEW.id, NEW.tenant, NEW.cloud_account_id, 'urgency',
                to_jsonb(v_old_val), to_jsonb(v_new_val), 'urgency_changed')
        ON CONFLICT (id) DO NOTHING;
    END IF;

    IF OLD.ends_at IS DISTINCT FROM NEW.ends_at THEN
        v_history_id := encode(sha256(
            convert_to(NEW.id || '|ends_at|' || COALESCE(OLD.ends_at::text,'') || '|' || COALESCE(NEW.ends_at::text,''), 'UTF8')
        ), 'hex');
        v_history_id := left(v_history_id, 32);

        INSERT INTO event_history (id, event_id, tenant_id, cloud_account_id, change_type, old_value, new_value, change_reason)
        VALUES (v_history_id, NEW.id, NEW.tenant, NEW.cloud_account_id, 'ends_at',
                to_jsonb(OLD.ends_at::text), to_jsonb(NEW.ends_at::text), 'event_resolved')
        ON CONFLICT (id) DO NOTHING;
    END IF;

    RETURN NEW;
EXCEPTION WHEN OTHERS THEN
    RAISE WARNING 'event_history trigger failed: %', SQLERRM;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
