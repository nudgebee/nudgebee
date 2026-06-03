-- Drop index
DROP INDEX IF EXISTS public.idx_anomaly_insights;

-- Drop insights column from anomaly table
ALTER TABLE public.anomaly DROP COLUMN IF EXISTS insights;
