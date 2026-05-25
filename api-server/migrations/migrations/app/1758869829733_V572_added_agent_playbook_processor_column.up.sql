
CREATE TABLE "public"."agent_playbook_action_processor" ("name" text NOT NULL, "value" text NOT NULL, PRIMARY KEY ("name") );

INSERT INTO "public"."agent_playbook_action_processor"("name", "value") VALUES (E'nb_server', E'nb_server');

INSERT INTO "public"."agent_playbook_action_processor"("name", "value") VALUES (E'nb_agent', E'nb_agent');

alter table "public"."agent_playbook_action_processor" rename to "agent_playbook_processor";

alter table "public"."agent_playbook" add column "processor" text
 null default 'nb_agent';

alter table "public"."agent_playbook_processor" add constraint "agent_playbook_processor_value_key" unique ("value");

alter table "public"."agent_playbook"
  add constraint "agent_playbook_processor_fkey"
  foreign key ("processor")
  references "public"."agent_playbook_processor"
  ("value") on update restrict on delete restrict;
