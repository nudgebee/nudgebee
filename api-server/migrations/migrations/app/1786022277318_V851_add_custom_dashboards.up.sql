-- Custom Dashboards: user-authored dashboards over the existing Grafana-shaped
-- panel model. `definition` holds the whole panel document (panels are always
-- read/written as a unit, never queried by inner field), so it is NOT normalised
-- into a panels table. `schema_version` lets the JSON shape evolve with a
-- code-side upgrader instead of a data migration over JSONB.
--
-- A dashboard has NO account of its own. Every panel carries its own
-- `account_id` inside `definition` (the metrics/logs/traces backends resolve the
-- provider integration per account), so one dashboard can put two clusters
-- side by side. The dashboard row is therefore a tenant-level document.

CREATE TABLE IF NOT EXISTS dashboards (
    id             uuid PRIMARY KEY NOT NULL DEFAULT gen_random_uuid(),
    tenant_id      uuid NOT NULL,
    slug           text NOT NULL,
    title          text NOT NULL,
    description    text,
    definition     jsonb NOT NULL DEFAULT '{"panels":[]}'::jsonb,
    schema_version integer NOT NULL DEFAULT 1,
    tags           jsonb NOT NULL DEFAULT '[]'::jsonb,
    status         varchar(50) NOT NULL DEFAULT 'active',
    is_builtin     boolean NOT NULL DEFAULT false,
    created_by     uuid,
    updated_by     uuid,
    created_at     timestamp without time zone NOT NULL DEFAULT now(),
    updated_at     timestamp without time zone NOT NULL DEFAULT now()
);

ALTER TABLE public.dashboards DROP CONSTRAINT IF EXISTS dashboards_tenant_id_fkey;
ALTER TABLE public.dashboards ADD CONSTRAINT dashboards_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE public.dashboards DROP CONSTRAINT IF EXISTS dashboards_created_by_fkey;
ALTER TABLE public.dashboards ADD CONSTRAINT dashboards_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

ALTER TABLE public.dashboards DROP CONSTRAINT IF EXISTS dashboards_updated_by_fkey;
ALTER TABLE public.dashboards ADD CONSTRAINT dashboards_updated_by_fkey FOREIGN KEY (updated_by) REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_dashboards_tenant_slug ON public.dashboards (tenant_id, slug);
CREATE INDEX IF NOT EXISTS idx_dashboards_tenant_status ON public.dashboards (tenant_id, status);

DROP TRIGGER IF EXISTS set_public_dashboards_updated_at ON public.dashboards;
CREATE TRIGGER set_public_dashboards_updated_at BEFORE UPDATE ON public.dashboards FOR EACH ROW EXECUTE FUNCTION set_current_timestamp_updated_at();


-- Immutable revision history. A JSONB blob edited in place has no undo, and
-- "who broke my panel" is the single most common dashboard support ticket.
CREATE TABLE IF NOT EXISTS dashboard_versions (
    id             uuid PRIMARY KEY NOT NULL DEFAULT gen_random_uuid(),
    dashboard_id   uuid NOT NULL,
    version        integer NOT NULL,
    definition     jsonb NOT NULL,
    schema_version integer NOT NULL DEFAULT 1,
    message        text,
    created_by     uuid,
    created_at     timestamp without time zone NOT NULL DEFAULT now()
);

ALTER TABLE public.dashboard_versions DROP CONSTRAINT IF EXISTS dashboard_versions_dashboard_id_fkey;
ALTER TABLE public.dashboard_versions ADD CONSTRAINT dashboard_versions_dashboard_id_fkey FOREIGN KEY (dashboard_id) REFERENCES public.dashboards(id) ON DELETE CASCADE ON UPDATE RESTRICT;

ALTER TABLE public.dashboard_versions DROP CONSTRAINT IF EXISTS dashboard_versions_created_by_fkey;
ALTER TABLE public.dashboard_versions ADD CONSTRAINT dashboard_versions_created_by_fkey FOREIGN KEY (created_by) REFERENCES public.users(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

CREATE UNIQUE INDEX IF NOT EXISTS uq_dashboard_versions ON public.dashboard_versions (dashboard_id, version);


-- Class A (contextual) resolution: which dashboards auto-attach to an entity.
-- Kept OUT of `definition` because it is queried on every detail-page load and
-- cannot be indexed inside a JSONB blob at acceptable cost.
CREATE TABLE IF NOT EXISTS dashboard_bindings (
    id           uuid PRIMARY KEY NOT NULL DEFAULT gen_random_uuid(),
    dashboard_id uuid NOT NULL,
    tenant_id    uuid NOT NULL,
    scope_type   text NOT NULL,
    match_kind   text NOT NULL,
    match_value  jsonb NOT NULL DEFAULT '{}'::jsonb,
    priority     integer NOT NULL DEFAULT 100,
    created_at   timestamp without time zone NOT NULL DEFAULT now()
);

ALTER TABLE public.dashboard_bindings DROP CONSTRAINT IF EXISTS dashboard_bindings_dashboard_id_fkey;
ALTER TABLE public.dashboard_bindings ADD CONSTRAINT dashboard_bindings_dashboard_id_fkey FOREIGN KEY (dashboard_id) REFERENCES public.dashboards(id) ON DELETE CASCADE ON UPDATE RESTRICT;

ALTER TABLE public.dashboard_bindings DROP CONSTRAINT IF EXISTS dashboard_bindings_tenant_id_fkey;
ALTER TABLE public.dashboard_bindings ADD CONSTRAINT dashboard_bindings_tenant_id_fkey FOREIGN KEY (tenant_id) REFERENCES public.tenant(id) ON DELETE RESTRICT ON UPDATE RESTRICT;

CREATE INDEX IF NOT EXISTS idx_dashboard_bindings_lookup ON public.dashboard_bindings (tenant_id, scope_type, match_kind);
CREATE INDEX IF NOT EXISTS idx_dashboard_bindings_dashboard ON public.dashboard_bindings (dashboard_id);


-- Provenance for dashboards pulled from Grafana / Datadog / raw JSON upload.
-- import_warnings keeps the lossy-translation report readable after import.
CREATE TABLE IF NOT EXISTS dashboard_imports (
    dashboard_id       uuid PRIMARY KEY NOT NULL,
    source_type        text NOT NULL,
    integration_id     uuid,
    remote_uid         text,
    remote_version     text,
    datasource_mapping jsonb NOT NULL DEFAULT '{}'::jsonb,
    import_warnings    jsonb NOT NULL DEFAULT '[]'::jsonb,
    last_synced_at     timestamp without time zone,
    created_at         timestamp without time zone NOT NULL DEFAULT now()
);

ALTER TABLE public.dashboard_imports DROP CONSTRAINT IF EXISTS dashboard_imports_dashboard_id_fkey;
ALTER TABLE public.dashboard_imports ADD CONSTRAINT dashboard_imports_dashboard_id_fkey FOREIGN KEY (dashboard_id) REFERENCES public.dashboards(id) ON DELETE CASCADE ON UPDATE RESTRICT;

CREATE INDEX IF NOT EXISTS idx_dashboard_imports_source ON public.dashboard_imports (source_type, integration_id);


CREATE TABLE IF NOT EXISTS dashboard_favorites (
    user_id      uuid NOT NULL,
    dashboard_id uuid NOT NULL,
    created_at   timestamp without time zone NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, dashboard_id)
);

ALTER TABLE public.dashboard_favorites DROP CONSTRAINT IF EXISTS dashboard_favorites_user_id_fkey;
ALTER TABLE public.dashboard_favorites ADD CONSTRAINT dashboard_favorites_user_id_fkey FOREIGN KEY (user_id) REFERENCES public.users(id) ON DELETE CASCADE ON UPDATE RESTRICT;

ALTER TABLE public.dashboard_favorites DROP CONSTRAINT IF EXISTS dashboard_favorites_dashboard_id_fkey;
ALTER TABLE public.dashboard_favorites ADD CONSTRAINT dashboard_favorites_dashboard_id_fkey FOREIGN KEY (dashboard_id) REFERENCES public.dashboards(id) ON DELETE CASCADE ON UPDATE RESTRICT;

-- The primary key is (user_id, dashboard_id), so dashboard_id is not a leading
-- column and cannot serve the cascade. Without this, deleting one dashboard
-- seq-scans every favorite row in the tenant.
CREATE INDEX IF NOT EXISTS idx_dashboard_favorites_dashboard ON public.dashboard_favorites (dashboard_id);
