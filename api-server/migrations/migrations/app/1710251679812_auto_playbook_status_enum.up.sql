INSERT INTO "public"."auto_playbook_status"("value", "description") VALUES (E'Disabled', E'the auto playbook is in disabled') ON CONFLICT DO NOTHING;
