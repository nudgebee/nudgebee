
alter table "public"."tenant_users" add column "is_default" boolean
 not null default 'false';

alter table "public"."tenant_users" add column "is_owner" boolean
 not null default 'false';
