ALTER TABLE public.event_log_analysis
DROP CONSTRAINT IF EXISTS event_log_analysis_event_id_analysis_type_key;

CREATE INDEX IF NOT EXISTS idx_event_log_analysis_event_id_analysis_type
ON public.event_log_analysis (event_id, analysis_type);
