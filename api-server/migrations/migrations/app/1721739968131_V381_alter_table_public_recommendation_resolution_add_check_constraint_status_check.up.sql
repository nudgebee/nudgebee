
alter table "public"."recommendation_resolution" drop constraint "status_check";
alter table "public"."recommendation_resolution" add constraint "status_check" check (status = ANY (ARRAY['InProgress'::text, 'Failed'::text, 'Success'::text, 'Configuring'::text]));
