DROP INDEX IF EXISTS idx_event_log_analysis_event_id_analysis_type;

ALTER TABLE public.event_log_analysis
ADD CONSTRAINT event_log_analysis_event_id_analysis_type_key UNIQUE (event_id, analysis_type);
