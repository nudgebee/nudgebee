
alter table "public"."notification_user" drop constraint "notification_user_status_fkey";

DELETE FROM "public"."notification_user_status_type" WHERE "value" = 'CLOSED';

DELETE FROM "public"."notification_user_status_type" WHERE "value" = 'OPEN';

DROP TABLE "public"."notification_user_status_type";

DROP TABLE "public"."notification_user";

alter table "public"."notifications" drop constraint "notifications_severity_fkey";

DELETE FROM "public"."notification_severity_type" WHERE "value" = 'Info';

DELETE FROM "public"."notification_severity_type" WHERE "value" = 'Low';

DELETE FROM "public"."notification_severity_type" WHERE "value" = 'Medium';

DELETE FROM "public"."notification_severity_type" WHERE "value" = 'High';

DELETE FROM "public"."notification_severity_type" WHERE "value" = 'Critical';

DROP TABLE "public"."notification_severity_type";

DROP TABLE "public"."notifications";
