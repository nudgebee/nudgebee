INSERT INTO public.recommendation_severity_type (value) SELECT 'Critical' ON CONFLICT DO NOTHING ;
