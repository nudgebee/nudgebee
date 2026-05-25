
alter table "public"."events" drop constraint "events_nb_status_check";
alter table "public"."events" add constraint "events_nb_status_check" check (CHECK (nb_status = ANY (ARRAY['OPEN'::text, 'ACKNOWLEDGED'::text, 'INVESTIGATING'::text, 'SNOOZED'::text, 'SUPPRESSED'::text, 'DROPPED'::text, 'RESOLVED'::text])));
