

CREATE TABLE "public"."auto_playbook_status" ("value" text NOT NULL default 'Active', "description" text NOT NULL, PRIMARY KEY ("value") );COMMENT ON TABLE "public"."auto_playbook_status" IS E'status enum for auto playbook';

alter table "public"."auto_playbook"
  add constraint "auto_playbook_status_fkey"
  foreign key ("status")
  references "public"."auto_playbook_status"
  ("value") on update restrict on delete restrict;


INSERT INTO "public"."auto_playbook_status"("value", "description") VALUES (E'Active', E'the auto playbook is in active state');

INSERT INTO "public"."auto_pilot_execution_status"("value", "description") VALUES (E'Disabled', E'the auto playbook is in disabled');


