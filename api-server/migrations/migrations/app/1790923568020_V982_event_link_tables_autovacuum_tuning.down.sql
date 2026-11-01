ALTER TABLE public.event_duplicates RESET (autovacuum_vacuum_scale_factor);
ALTER TABLE public.event_correlations RESET (autovacuum_vacuum_scale_factor);

DO $$
BEGIN
  IF current_setting('server_version_num')::int >= 130000 THEN
    EXECUTE 'ALTER TABLE public.event_duplicates RESET (autovacuum_vacuum_insert_scale_factor)';
    EXECUTE 'ALTER TABLE public.event_correlations RESET (autovacuum_vacuum_insert_scale_factor)';
  END IF;
END $$;
