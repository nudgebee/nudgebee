INSERT INTO public.integration_types (category, description, name)
VALUES ('database', NULL, 'ES')
ON CONFLICT (name) DO NOTHING;
