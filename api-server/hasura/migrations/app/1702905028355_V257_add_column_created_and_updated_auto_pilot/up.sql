
alter table "public"."auto_pilot_task" add column "created_at" timestamp
 not null default now();

alter table "public"."auto_pilot_task" add column "updated_at" timestamp
 not null default now();
