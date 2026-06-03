
alter table "public"."runbook_action" add column "internal_identifier" text
 not null default 'custom_image_action';
