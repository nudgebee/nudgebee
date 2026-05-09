
CREATE INDEX if not exists events_tenant_account_starts_idx
          ON public.events (tenant, cloud_account_id, starts_at);


CREATE index if not exists events_tenant_account_type_aggregation_idx ON public.events (cloud_account_id,tenant,finding_type,aggregation_key);




