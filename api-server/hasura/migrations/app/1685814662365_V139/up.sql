
CREATE TABLE "public"."tenant_type" ("value" text NOT NULL, PRIMARY KEY ("value") );

INSERT INTO "public"."tenant_type"("value") VALUES (E'QA');

INSERT INTO "public"."tenant_type"("value") VALUES (E'Demo');

INSERT INTO "public"."tenant_type"("value") VALUES (E'Customer');

alter table "public"."tenant" add column "type" Text
 null default 'Customer';

alter table "public"."tenant"
  add constraint "tenant_type_fkey"
  foreign key ("type")
  references "public"."tenant_type"
  ("value") on update restrict on delete restrict;
