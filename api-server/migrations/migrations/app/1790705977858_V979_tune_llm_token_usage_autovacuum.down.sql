ALTER TABLE public.llm_conversation_token_usage RESET (
  autovacuum_vacuum_scale_factor,
  autovacuum_analyze_scale_factor
);

DO $$
BEGIN
  IF current_setting('server_version_num')::int >= 130000 THEN
    EXECUTE 'ALTER TABLE public.llm_conversation_token_usage RESET (autovacuum_vacuum_insert_scale_factor)';
  END IF;
END $$;
