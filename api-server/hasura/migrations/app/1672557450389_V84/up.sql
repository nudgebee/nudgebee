
CREATE TABLE "public"."recommendation_action_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."recommendation_action_type"("value") VALUES (E'Modify');

alter table "public"."recommendation"
  add constraint "recommendation_recommendation_action_fkey"
  foreign key ("recommendation_action")
  references "public"."recommendation_action_type"
  ("value") on update restrict on delete restrict;
