alter table
  "public"."auto_playbook_executions"
add
  column "skipped_by" uuid null;

alter table
  "public"."auto_playbook_executions"
add
  constraint "auto_playbook_executions_skipped_by_fkey" foreign key ("skipped_by") references "public"."users" ("id") on update restrict on delete restrict;

update
  auto_playbook_executions
set
  skipped_by = (
    "attribute" -> 'trigger_context' ->> 'skipped_by'
  ) :: uuid
where
  "attribute" -> 'trigger_context' -> 'skipped_by' is not null;