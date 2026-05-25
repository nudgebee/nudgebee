
alter table "public"."recommendation" drop constraint "recommendation_recommendation_action_fkey";

DELETE FROM "public"."recommendation_action_type" WHERE "value" = 'Modify';

DROP TABLE "public"."recommendation_action_type";
