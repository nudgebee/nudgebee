alter table "public"."llm_conversations"
  add constraint "llm_conversations_tenant_id_fkey"
  foreign key ("tenant_id")
  references "public"."tenant"
  ("id") on update restrict on delete restrict;
