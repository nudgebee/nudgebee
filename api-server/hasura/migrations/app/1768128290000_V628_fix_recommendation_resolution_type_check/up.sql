

alter table "public"."recommendation_resolution" drop constraint "type_check";
alter table "public"."recommendation_resolution" add constraint "type_check" check (type = ANY (ARRAY['PullRequest'::text, 'Ticket'::text, 'DeploymentChange'::text, 'EventResolution'::text, 'CloudResource'::text]));
