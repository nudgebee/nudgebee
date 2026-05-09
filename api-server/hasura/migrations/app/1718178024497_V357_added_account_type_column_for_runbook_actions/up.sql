
alter table "public"."runbook_action" add column "account_type" text
 not null default 'K8s';
