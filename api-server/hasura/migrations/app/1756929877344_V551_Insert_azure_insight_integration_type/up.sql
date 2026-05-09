INSERT INTO public.integration_types (category, description, name)
VALUES ('observability_platform', NULL, 'azure_app_insights')
ON CONFLICT (name) DO NOTHING;
