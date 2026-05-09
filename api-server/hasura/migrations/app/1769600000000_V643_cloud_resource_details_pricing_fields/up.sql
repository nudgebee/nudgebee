-- Add provider-agnostic pricing columns to cloud_resource_details
-- Supports AWS EC2/RDS, Azure VM/SQL/Storage, GCP CE, Civo

-- Compute attributes
ALTER TABLE cloud_resource_details
  ADD COLUMN IF NOT EXISTS architecture text,
  ADD COLUMN IF NOT EXISTS gpu_count integer DEFAULT 0,
  ADD COLUMN IF NOT EXISTS network_performance text,
  ADD COLUMN IF NOT EXISTS operating_system text,
  ADD COLUMN IF NOT EXISTS tenancy text,
  ADD COLUMN IF NOT EXISTS current_generation boolean DEFAULT true;

-- Database attributes (AWS RDS, Azure SQL, GCP Cloud SQL)
ALTER TABLE cloud_resource_details
  ADD COLUMN IF NOT EXISTS database_engine text NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS deployment_option text NOT NULL DEFAULT '';

-- Storage/Disk attributes (Azure Managed Disks, AWS EBS, GCP PD)
ALTER TABLE cloud_resource_details
  ADD COLUMN IF NOT EXISTS storage_type text;

-- Pricing normalization
ALTER TABLE cloud_resource_details
  ADD COLUMN IF NOT EXISTS price_unit text NOT NULL DEFAULT 'hourly',
  ADD COLUMN IF NOT EXISTS pricing_model text NOT NULL DEFAULT 'on_demand';

-- Update unique constraint to support multiple pricing variants per resource type
-- e.g. PostgreSQL Single-AZ vs PostgreSQL Multi-AZ, on_demand vs spot
ALTER TABLE cloud_resource_details
  DROP CONSTRAINT IF EXISTS cloud_resource_details_service_name_resource_region_resource_key;

ALTER TABLE cloud_resource_details
  ADD CONSTRAINT cloud_resource_details_unique_key
  UNIQUE (cloud_provider, service_name, service_type, resource_type, resource_region,
          pricing_model, database_engine, deployment_option);

-- Index for fast lookups by cloud-collector
CREATE INDEX IF NOT EXISTS idx_crd_provider_type_region
  ON cloud_resource_details (cloud_provider, service_type, resource_region, resource_type);

-- Delete GCP instances misclassified as AWS (282 rows from past bug)
DELETE FROM cloud_resource_details
WHERE cloud_provider = 'aws' AND resource_type LIKE 'c3d-%';

-- Backfill new columns from existing attributes JSONB for AWS data
UPDATE cloud_resource_details
SET
  architecture = attributes->>'processorArchitecture',
  network_performance = attributes->>'networkPerformance',
  tenancy = attributes->>'tenancy',
  operating_system = COALESCE(attributes->>'operatingSystem', 'Linux'),
  database_engine = COALESCE(attributes->>'databaseEngine', ''),
  deployment_option = COALESCE(attributes->>'deploymentOption', ''),
  current_generation = CASE WHEN attributes->>'currentGeneration' = 'Yes' THEN true ELSE false END
WHERE cloud_provider = 'aws' AND attributes IS NOT NULL AND attributes::text != '{}';

-- Backfill defaults for non-AWS providers
UPDATE cloud_resource_details
SET
  operating_system = 'Linux',
  pricing_model = 'on_demand',
  price_unit = 'hourly'
WHERE cloud_provider != 'aws' AND operating_system IS NULL;
