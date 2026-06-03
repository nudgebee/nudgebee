
CREATE TABLE "public"."auto_pilot_task_status" (
    "value" text NOT NULL DEFAULT 'Scheduled',
    "description" text,
    PRIMARY KEY ("value")
);

-- COMMENT ON TABLE "public"."auto_pilot_task_status" IS E 'status for auto pilot tasks ';
INSERT INTO "public"."auto_pilot_task_status"("value", "description") VALUES (E'Scheduled', E'the task is scheduled but not executed');

alter table "public"."auto_pilot_task" add column "status" text
 not null default 'Scheduled';

alter table "public"."auto_pilot_task"
  add constraint "auto_pilot_task_status_fkey"
  foreign key ("status")
  references "public"."auto_pilot_task_status"
  ("value") on update restrict on delete restrict;
