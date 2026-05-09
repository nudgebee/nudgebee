-- Reverse: drop new columns and restore old constraint

DROP INDEX IF EXISTS idx_crd_provider_type_region;

ALTER TABLE cloud_resource_details
  DROP CONSTRAINT IF EXISTS cloud_resource_details_unique_key;

ALTER TABLE cloud_resource_details
  ADD CONSTRAINT cloud_resource_details_service_name_resource_region_resource_key
  UNIQUE (service_name, resource_region, resource_type, cloud_provider, service_type);

ALTER TABLE cloud_resource_details
  DROP COLUMN IF EXISTS architecture,
  DROP COLUMN IF EXISTS gpu_count,
  DROP COLUMN IF EXISTS network_performance,
  DROP COLUMN IF EXISTS operating_system,
  DROP COLUMN IF EXISTS tenancy,
  DROP COLUMN IF EXISTS current_generation,
  DROP COLUMN IF EXISTS database_engine,
  DROP COLUMN IF EXISTS deployment_option,
  DROP COLUMN IF EXISTS storage_type,
  DROP COLUMN IF EXISTS price_unit,
  DROP COLUMN IF EXISTS pricing_model;
