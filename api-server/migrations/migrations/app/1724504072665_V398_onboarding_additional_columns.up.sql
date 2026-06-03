
alter table "public"."tenant_onboarding" add column "user_displayname" text
 not null;

alter table "public"."tenant_onboarding" add column "tenant_name" text
 not null;

alter table "public"."tenant_onboarding" add column "contact_phone" text
 not null;

alter table "public"."tenant_onboarding" alter column "contact_phone" drop not null;
