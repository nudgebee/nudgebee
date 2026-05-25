
CREATE TABLE "public"."auto_playbook_executions" ("id" uuid NOT NULL DEFAULT gen_random_uuid(), "auto_playbook_id" uuid NOT NULL, "status" text NOT NULL, "created_at" timestamp NOT NULL DEFAULT now(), "updated_at" timestamp, PRIMARY KEY ("id") , FOREIGN KEY ("auto_playbook_id") REFERENCES "public"."auto_playbook"("id") ON UPDATE restrict ON DELETE restrict, UNIQUE ("id"));
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE "public"."auto_playbook_execution_status" ("values" text NOT NULL, "description" text NOT NULL, PRIMARY KEY ("values") );

INSERT INTO "public"."auto_playbook_execution_status"("values", "description") VALUES (E'in_progress', E'the execution is in progress');

INSERT INTO "public"."auto_playbook_execution_status"("values", "description") VALUES (E'complete', E'the execution is in complete');

INSERT INTO "public"."auto_playbook_execution_status"("values", "description") VALUES (E'failed', E'the execution is in complete');

alter table "public"."auto_playbook_executions" alter column "status" set default 'in_progress';
