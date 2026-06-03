-- Rollback: Remove PostgreSQL audit triggers (Hasura event triggers will be re-enabled)

DROP TRIGGER IF EXISTS audit_messaging_platforms_insert_pg ON messaging_platforms;
DROP TRIGGER IF EXISTS audit_jira_configurations_pg ON jira_configurations;
DROP TRIGGER IF EXISTS audit_tickets_pg ON tickets;
DROP TRIGGER IF EXISTS audit_auto_pilot_pg ON auto_pilot;
DROP TRIGGER IF EXISTS audit_auto_playbook_pg ON auto_playbook;
DROP TRIGGER IF EXISTS event_history_changes_pg ON events;

DROP FUNCTION IF EXISTS fn_audit_trigger();
DROP FUNCTION IF EXISTS fn_event_history_trigger();
