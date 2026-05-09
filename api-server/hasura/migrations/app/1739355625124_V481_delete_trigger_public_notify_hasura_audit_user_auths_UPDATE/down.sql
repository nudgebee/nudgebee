
CREATE TRIGGER "notify_hasura_audit_user_auths_UPDATE"
AFTER UPDATE ON "public"."user_auths"
FOR EACH ROW EXECUTE FUNCTION hdb_catalog."notify_hasura_audit_user_auths_UPDATE"();
