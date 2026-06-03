alter table "public"."recommendation" add column "estimated_savings" float8 default 0;
alter table "public"."recommendation" add column "severity" text default 'Medium';
alter table "public"."recommendation" add column "status" text default 'Open';

alter table "public"."recommendation"
  add constraint "recommendation_severity_fkey"
  foreign key ("severity")
  references "public"."recommendation_severity_type"
  ("value") on update cascade on delete no action;

alter table "public"."recommendation"
  add constraint "recommendation_status_fkey"
  foreign key (status)
  references "public"."recommendation_status_type"
  ("value") on update cascade on delete no action;


alter table "public"."recommendation" add column "category" text
 null default 'RightSizing';

CREATE TABLE "public"."recommendation_category_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."recommendation_category_type"("value") VALUES (E'RightSizing');

INSERT INTO "public"."recommendation_category_type"("value") VALUES (E'Security');

INSERT INTO "public"."recommendation_category_type"("value") VALUES (E'InfraUpgrade');

alter table "public"."recommendation"
  add constraint "recommendation_category_fkey"
  foreign key ("category")
  references "public"."recommendation_category_type"
  ("value") on update restrict on delete restrict;
