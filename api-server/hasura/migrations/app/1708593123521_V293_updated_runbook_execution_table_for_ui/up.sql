
alter table "public"."auto_playbook_executions" add column "account_id" uuid
 null;

alter table "public"."auto_playbook_executions" add column "tenant_id" uuid
 null;

alter table "public"."auto_playbook_executions" add column "meta" jsonb
 null default jsonb_build_object();

update
    auto_playbook_executions
set
    tenant_id = (
        select
            tenant_id
        from
            auto_playbook ap
        where
            ap.id = auto_playbook_executions.auto_playbook_id
    ),
    account_id = (
        select
            account_id
        from
            auto_playbook ap
        where
            ap.id = auto_playbook_executions.auto_playbook_id
    );
