
alter table "public"."events" add column "principal" text
 null;

DROP INDEX IF EXISTS "public"."events_id_findingid_tenant";

CREATE UNIQUE INDEX "events_cloudaccount_findingid" on
  "public"."events" using btree ("tenant", "cloud_account_id", "finding_id");


insert into event_source values ('AWS_CloudTrail')
  on conflict do nothing;
