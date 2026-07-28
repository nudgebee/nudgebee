-- Revert to the original single audit_auto_pilot_pg trigger (V700): audit every
-- INSERT/UPDATE/DELETE on auto_pilot with no field filter.

DROP TRIGGER IF EXISTS audit_auto_pilot_upd_pg ON auto_pilot;
DROP TRIGGER IF EXISTS audit_auto_pilot_ins_del_pg ON auto_pilot;

CREATE TRIGGER audit_auto_pilot_pg
    AFTER INSERT OR UPDATE OR DELETE ON auto_pilot
    FOR EACH ROW EXECUTE FUNCTION fn_audit_trigger(
        'AUTO_PILOT',
        'AUTOPILOT_CREATE', 'AUTOPILOT_UPDATE', 'AUTOPILOT_DELETE'
    );
