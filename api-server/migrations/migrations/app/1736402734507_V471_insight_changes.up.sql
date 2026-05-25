
alter table "public"."insight" add column "applications" jsonb
 null;

UPDATE insight SET status = 'CLOSED';
