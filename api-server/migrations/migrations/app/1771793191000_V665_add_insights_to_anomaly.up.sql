-- Add insights column to anomaly table
ALTER TABLE public.anomaly
ADD COLUMN IF NOT EXISTS insights JSONB DEFAULT '[]'::jsonb;

-- Create GIN index for faster queries on insights
CREATE INDEX IF NOT EXISTS idx_anomaly_insights ON public.anomaly USING GIN (insights);

-- Add comment describing the column
COMMENT ON COLUMN public.anomaly.insights IS 'Array of insight objects for each detected anomaly, containing human-readable explanations with deviation percentages and severity levels';
