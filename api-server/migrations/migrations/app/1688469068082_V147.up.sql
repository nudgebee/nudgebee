
INSERT INTO "public"."recommendation_status_type"("value", "description") VALUES (E'Closed', E'The recommendation which are applied by the user');

INSERT INTO "public"."recommendation_status_type"("description", "value") VALUES (E'The recommendation which are no longer useful for user', E'Dismissed');

INSERT INTO "public"."recommendation_status_type"("description", "value") VALUES (E'The recomendation which are assigned to in jira ticket', E'Assigned');

alter table "public"."recommendation" add column "dismissed_reason" text
 null;
