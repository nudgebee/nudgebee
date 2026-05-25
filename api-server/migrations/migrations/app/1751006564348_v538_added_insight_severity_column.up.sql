
alter table "public"."insight" add column "severity" text
 null;

CREATE TABLE "public"."insight_severity" ("value" text NOT NULL, "comment" text NOT NULL, PRIMARY KEY ("value") , UNIQUE ("value"));

INSERT INTO "public"."insight_severity"("value", "comment") VALUES (E'Low', E'Low');

INSERT INTO "public"."insight_severity"("value", "comment") VALUES (E'Medium', E'Medium');

INSERT INTO "public"."insight_severity"("value", "comment") VALUES (E'High', E'High');

INSERT INTO "public"."insight_severity"("value", "comment") VALUES (E'Critical', E'Critical');

alter table "public"."insight"
  add constraint "insight_severity_fkey"
  foreign key ("severity")
  references "public"."insight_severity"
  ("value") on update restrict on delete restrict;
