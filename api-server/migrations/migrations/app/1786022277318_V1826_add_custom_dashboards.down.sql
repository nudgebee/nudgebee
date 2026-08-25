DROP TABLE IF EXISTS public.dashboard_favorites;
DROP TABLE IF EXISTS public.dashboard_imports;
DROP TABLE IF EXISTS public.dashboard_bindings;
DROP TABLE IF EXISTS public.dashboard_versions;
DROP TRIGGER IF EXISTS set_public_dashboards_updated_at ON public.dashboards;
DROP TABLE IF EXISTS public.dashboards;
