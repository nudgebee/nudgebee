-- Drop the old truncated constraint that wasn't removed by V643
-- (PostgreSQL truncated the name to 63 chars)
ALTER TABLE cloud_resource_details
  DROP CONSTRAINT IF EXISTS cloud_resource_details_service_name_resource_region_resource_ty;
