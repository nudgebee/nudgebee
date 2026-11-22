/**
 * What a cloud metrics query needs to find one resource's series.
 *
 * Providers key metrics by their own resource id (`resource_id` on a
 * cloud_resource_v2 row): an EC2 instance id, an RDS DB identifier, an Azure
 * ARM URI, a numeric Compute Engine id, `project:name` for Cloud SQL. Never
 * the row's UUID, and never its display name, which differs from the id for a
 * tagged EC2 instance and for every Azure and GCP resource. ECS tasks and
 * services are queried by ARN. CloudWatch also needs the resource's region.
 * Same choice the collector makes when it stores metrics (metricIdentifierFor).
 */
export interface CloudMetricsTarget {
  resourceId: string;
  serviceName: string;
  region: string;
}

const ARN_KEYED_SERVICES = new Set(['amazonecs', 'awsfargate']);
const ARN_KEYED_TYPES = new Set(['task', 'service']);

export function cloudMetricsTarget(resource: any): CloudMetricsTarget | null {
  const providerId = resource?.resource_id || '';
  if (!providerId) return null;
  const serviceName = resource.service_name || '';
  const byArn =
    Boolean(resource.arn) && ARN_KEYED_SERVICES.has(serviceName.toLowerCase()) && ARN_KEYED_TYPES.has((resource.type || '').toLowerCase());
  return {
    resourceId: byArn ? resource.arn : providerId,
    serviceName,
    region: resource.region || '',
  };
}
