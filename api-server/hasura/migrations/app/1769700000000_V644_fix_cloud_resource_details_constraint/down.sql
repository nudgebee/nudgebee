-- Restore the old unique constraint
ALTER TABLE cloud_resource_details
  ADD CONSTRAINT cloud_resource_details_service_name_resource_region_resource_ty
  UNIQUE (service_name, resource_region, resource_type, cloud_provider, service_type);
