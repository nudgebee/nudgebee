-- fn_event_history_trigger (V700) has been silently failing on every
-- priority/status/urgency/ends_at change since it was merged: v_history_id is
-- a TEXT (a truncated sha256 hex digest) and event_history.id is uuid.
-- Postgres does not implicitly cast a text variable to uuid on INSERT, so
-- every attempt threw "column "id" is of type uuid but expression is of type
-- text" — caught by the trigger's own EXCEPTION WHEN OTHERS handler, which
-- logs a WARNING and returns NEW, so the parent UPDATE always succeeded and
-- nothing ever surfaced this as a query failure. event_history has therefore
-- never recorded a row from this trigger. Found by exercising it locally
-- while testing #36597's anomaly/SLO close paths.
--
-- Fix: cast v_history_id to ::uuid at each INSERT. No other logic changes.
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
    -- Only fire on UPDATE
    IF TG_OP != 'UPDATE' THEN
        RETURN NEW;
    END IF;

    -- Skip bulk closures: status changed to CLOSED and priority unchanged
    IF OLD.status IS DISTINCT FROM NEW.status
       AND NEW.status = 'CLOSED'
       AND NOT (OLD.priority IS DISTINCT FROM NEW.priority) THEN
        RETURN NEW;
    END IF;

    -- Track priority changes
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
        VALUES (v_history_id::uuid, NEW.id, NEW.tenant, NEW.cloud_account_id, 'priority',
                to_jsonb(v_old_val), to_jsonb(v_new_val), v_change_reason,
                CASE WHEN v_metadata = '{}'::jsonb THEN NULL ELSE v_metadata END)
        ON CONFLICT (id) DO NOTHING;
    END IF;

    -- Track status changes
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
        VALUES (v_history_id::uuid, NEW.id, NEW.tenant, NEW.cloud_account_id, 'status',
                to_jsonb(v_old_val), to_jsonb(v_new_val), v_change_reason)
        ON CONFLICT (id) DO NOTHING;
    END IF;

    -- Track urgency changes
    IF OLD.urgency IS DISTINCT FROM NEW.urgency THEN
        v_old_val := OLD.urgency;
        v_new_val := NEW.urgency;

        v_history_id := encode(sha256(
            convert_to(NEW.id || '|urgency|' || COALESCE(v_old_val,'') || '|' || COALESCE(v_new_val,''), 'UTF8')
        ), 'hex');
        v_history_id := left(v_history_id, 32);

        INSERT INTO event_history (id, event_id, tenant_id, cloud_account_id, change_type, old_value, new_value, change_reason)
        VALUES (v_history_id::uuid, NEW.id, NEW.tenant, NEW.cloud_account_id, 'urgency',
                to_jsonb(v_old_val), to_jsonb(v_new_val), 'urgency_changed')
        ON CONFLICT (id) DO NOTHING;
    END IF;

    -- Track ends_at changes (resolution)
    IF OLD.ends_at IS DISTINCT FROM NEW.ends_at THEN
        v_history_id := encode(sha256(
            convert_to(NEW.id || '|ends_at|' || COALESCE(OLD.ends_at::text,'') || '|' || COALESCE(NEW.ends_at::text,''), 'UTF8')
        ), 'hex');
        v_history_id := left(v_history_id, 32);

        INSERT INTO event_history (id, event_id, tenant_id, cloud_account_id, change_type, old_value, new_value, change_reason)
        VALUES (v_history_id::uuid, NEW.id, NEW.tenant, NEW.cloud_account_id, 'ends_at',
                to_jsonb(OLD.ends_at::text), to_jsonb(NEW.ends_at::text), 'event_resolved')
        ON CONFLICT (id) DO NOTHING;
    END IF;

    RETURN NEW;
EXCEPTION WHEN OTHERS THEN
    RAISE WARNING 'event_history trigger failed: %', SQLERRM;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
