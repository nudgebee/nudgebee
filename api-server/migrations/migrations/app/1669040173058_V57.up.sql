
alter table "public"."projects" add column "category" Text
 null default 'Internal';

CREATE TABLE "public"."project_category_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."project_category_type"("value") VALUES (E'Internal');

INSERT INTO "public"."project_category_type"("value") VALUES (E'Training');

INSERT INTO "public"."project_category_type"("value") VALUES (E'Delivery');

INSERT INTO "public"."project_category_type"("value") VALUES (E'ResearchAndDevelopment');

alter table "public"."projects"
  add constraint "projects_category_fkey"
  foreign key ("category")
  references "public"."project_category_type"
  ("value") on update restrict on delete restrict;
