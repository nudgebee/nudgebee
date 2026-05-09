INSERT INTO public.integration_types (category, description, name)
VALUES ('log', NULL, 'loggly')
ON CONFLICT (name) DO NOTHING;
INSERT INTO public.integration_types (category, description, name)
VALUES ('log', NULL, 'observe')
ON CONFLICT (name) DO NOTHING;
