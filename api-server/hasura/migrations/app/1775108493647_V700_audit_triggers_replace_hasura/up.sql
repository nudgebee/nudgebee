-- Migration: Replace Hasura event triggers with PostgreSQL triggers
-- for tables that are NOT written by api-server/services (written by other services).
-- Also replaces audit_event_changes with a PG trigger for event history tracking.

----------------------------------------------------------------------
-- 1. Generic audit trigger function
--    Inserts into the audit table for INSERT/UPDATE/DELETE on any table.
--    Extracts user_id, tenant_id, account_id from the row data.
----------------------------------------------------------------------
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

    -- Insert audit record
    INSERT INTO audit (
        user_id, tenant_id, account_id, event_time,
        event_category, event_type, event_prev_state, event_state,
        event_actor, event_target, event_action, event_status, event_attr
    ) VALUES (
        v_user_id, v_tenant_id, v_account_id, NOW(),
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

----------------------------------------------------------------------
-- 2. Create audit triggers for tables NOT written by api-server/services
--    These tables are modified by other services (ticket-server, notification-server, auto-pilot)
----------------------------------------------------------------------

-- messaging_platforms: INSERT comes from notification-server (UPDATE/DELETE covered by application code)
CREATE TRIGGER audit_messaging_platforms_insert_pg
    AFTER INSERT ON messaging_platforms
    FOR EACH ROW EXECUTE FUNCTION fn_audit_trigger(
        'NOTIFICATIONS',
        'MESSAGING_PLATFORM_CREATE', NULL, NULL
    );

-- jira_configurations: written by ticket-server
CREATE TRIGGER audit_jira_configurations_pg
    AFTER INSERT OR UPDATE OR DELETE ON jira_configurations
    FOR EACH ROW EXECUTE FUNCTION fn_audit_trigger(
        'TICKETS',
        'TICKET_CONFIGURATION_CREATE', 'TICKET_CONFIGURATION_UPDATE', 'TICKET_CONFIGURATION_DELETE'
    );

-- tickets: written by ticket-server
CREATE TRIGGER audit_tickets_pg
    AFTER INSERT OR UPDATE OR DELETE ON tickets
    FOR EACH ROW EXECUTE FUNCTION fn_audit_trigger(
        'TICKETS',
        'TICKET_CREATE', 'TICKET_UPDATE', 'TICKET_DELETE'
    );

-- auto_pilot: written by auto-pilot service / ml-k8s-server
CREATE TRIGGER audit_auto_pilot_pg
    AFTER INSERT OR UPDATE OR DELETE ON auto_pilot
    FOR EACH ROW EXECUTE FUNCTION fn_audit_trigger(
        'AUTO_PILOT',
        'AUTOPILOT_CREATE', 'AUTOPILOT_UPDATE', 'AUTOPILOT_DELETE'
    );

-- auto_playbook: written by auto-pilot service
CREATE TRIGGER audit_auto_playbook_pg
    AFTER INSERT OR UPDATE OR DELETE ON auto_playbook
    FOR EACH ROW EXECUTE FUNCTION fn_audit_trigger(
        'AUTO_RUNBOOK',
        'AUTORUNBOOK_CREATE', 'AUTORUNBOOK_UPDATE', 'AUTORUNBOOK_DELETE'
    );

----------------------------------------------------------------------
-- 3. Event history trigger (replaces audit_event_changes)
--    Tracks changes to priority, status, urgency, ends_at on events table.
----------------------------------------------------------------------
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
        VALUES (v_history_id, NEW.id, NEW.tenant, NEW.cloud_account_id, 'priority',
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
        VALUES (v_history_id, NEW.id, NEW.tenant, NEW.cloud_account_id, 'status',
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
        VALUES (v_history_id, NEW.id, NEW.tenant, NEW.cloud_account_id, 'urgency',
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

CREATE TRIGGER event_history_changes_pg
    AFTER UPDATE ON events
    FOR EACH ROW EXECUTE FUNCTION fn_event_history_trigger();
