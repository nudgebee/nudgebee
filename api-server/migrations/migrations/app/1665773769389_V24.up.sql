-- public.project_fundings definition

-- Drop table

DROP TABLE public.project_fundings;

CREATE TABLE public.project_fundings (
	id uuid NOT NULL DEFAULT gen_random_uuid(),
	project uuid NOT NULL,
	funding_source uuid NOT NULL,
	CONSTRAINT project_fundings_pkey PRIMARY KEY (id)
);


-- public.project_fundings foreign keys

ALTER TABLE public.project_fundings ADD CONSTRAINT project_fundings_funding_source_fkey FOREIGN KEY (funding_source) REFERENCES public.funding_sources(id) ON DELETE RESTRICT ON UPDATE RESTRICT;
ALTER TABLE public.project_fundings ADD CONSTRAINT project_fundings_project_fkey FOREIGN KEY (project) REFERENCES public.projects(id) ON DELETE CASCADE ON UPDATE CASCADE;


alter table "public"."project_fundings" drop column "funding_source" cascade;

alter table "public"."project_fundings" add column "businessunit_funding" uuid
 not null;

alter table "public"."project_fundings" add column "amount" float8
 null default '0.0';

alter table "public"."project_fundings" add column "planned_amount" float8
 null default '0.0';

alter table "public"."project_fundings" add column "created_at" timestamp
 null default now();

alter table "public"."project_fundings" add column "updated_at" timestamp
 not null default now();

alter table "public"."project_fundings" alter column "created_at" set not null;

alter table "public"."project_fundings" add column "created_by" uuid
 not null;

alter table "public"."project_fundings" add column "updated_by" uuid
 not null;

alter table "public"."project_fundings" add column "start_date" timestamp
 null;

alter table "public"."project_fundings" add column "end_date" timestamp
 null;

alter table "public"."project_fundings"
  add constraint "project_fundings_created_by_fkey"
  foreign key ("created_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."project_fundings"
  add constraint "project_fundings_updated_by_fkey"
  foreign key ("updated_by")
  references "public"."users"
  ("id") on update restrict on delete restrict;

alter table "public"."project_fundings"
  add constraint "project_fundings_businessunit_funding_fkey"
  foreign key ("businessunit_funding")
  references "public"."businessunit_funding"
  ("id") on update restrict on delete restrict;

alter table "public"."project_fundings" add constraint "project_fundings_businessunit_funding_project_key" unique ("businessunit_funding", "project");
