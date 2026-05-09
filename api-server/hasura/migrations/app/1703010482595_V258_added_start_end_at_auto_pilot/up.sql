
alter table "public"."auto_pilot" add column "start_at" timestamp
 not null default now();

alter table "public"."auto_pilot" add column "end_at" timestamp
 null;
