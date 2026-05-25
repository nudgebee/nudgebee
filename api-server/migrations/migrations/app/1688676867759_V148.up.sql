

alter table "public"."recommendation" add column "is_dismissed" boolean
 not null default 'false';
