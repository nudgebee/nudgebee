
DELETE FROM "public"."agent_playbook_trigger" WHERE "name" = 'on_prometheus_alert';

DROP TABLE "public"."agent_playbook";

DROP TABLE "public"."agent_playbook_action";

DROP TABLE "public"."agent_playbook_trigger";
