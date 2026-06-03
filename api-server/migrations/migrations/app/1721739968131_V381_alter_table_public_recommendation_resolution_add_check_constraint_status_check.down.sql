
alter table "public"."recommendation_resolution" drop constraint "status_check";
alter table "public"."recommendation_resolution" add constraint "status_check" check (CHECK (status = ANY (ARRAY['InProgress'::text, 'Failed'::text, 'Success'::text])));
