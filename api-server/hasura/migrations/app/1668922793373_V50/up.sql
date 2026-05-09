
alter table "public"."projects" add column "billable" boolean
 null default 'false';

alter table "public"."projects" add column "approved_by" uuid
 null;

alter table "public"."projects" add column "project_manager" uuid
 null;

alter table "public"."projects" add column "it_manager" uuid
 null;

alter table "public"."projects" add column "expected_revenue" float8
 null default '0.0';

alter table "public"."projects"
  add constraint "projects_project_manager_fkey"
  foreign key ("project_manager")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."projects"
  add constraint "projects_it_manager_fkey"
  foreign key ("it_manager")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."projects"
  add constraint "projects_approved_by_fkey"
  foreign key ("approved_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;
