import { cloudMetricsTarget } from '@components/optimise-new/evidence/cloudMetricsTarget';

describe('cloudMetricsTarget', () => {
  it('queries an EC2 instance by its instance id and region, not by its Name tag', () => {
    expect(
      cloudMetricsTarget({
        resource_id: 'i-0123456789abcdef0',
        name: 'example-db-node',
        arn: 'arn:aws:ec2:eu-west-1:000000000000:instance/i-0123456789abcdef0',
        type: 'instance',
        region: 'eu-west-1',
        service_name: 'AmazonEC2',
      })
    ).toEqual({ resourceId: 'i-0123456789abcdef0', serviceName: 'AmazonEC2', region: 'eu-west-1' });
  });

  it('queries an RDS instance by its DB identifier', () => {
    expect(cloudMetricsTarget({ resource_id: 'example-db', name: 'example-db', region: 'us-east-1', service_name: 'AmazonRDS' })).toEqual({
      resourceId: 'example-db',
      serviceName: 'AmazonRDS',
      region: 'us-east-1',
    });
  });

  it('queries an Azure resource by its ARM URI, not its short name', () => {
    const uri = '/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.Compute/virtualMachines/example-vm';
    expect(cloudMetricsTarget({ resource_id: uri, name: 'example-vm', region: 'eastus', service_name: 'Microsoft.Compute/virtualMachines' })).toEqual(
      { resourceId: uri, serviceName: 'Microsoft.Compute/virtualMachines', region: 'eastus' }
    );
  });

  it('queries GCP resources by their provider id, not their name', () => {
    expect(
      cloudMetricsTarget({ resource_id: '1234567890123456789', name: 'example-vm', region: 'us-central1-a', service_name: 'Compute Engine' })
        ?.resourceId
    ).toBe('1234567890123456789');
    expect(cloudMetricsTarget({ resource_id: 'example-project:example-sql', name: 'example-sql', service_name: 'Cloud SQL' })?.resourceId).toBe(
      'example-project:example-sql'
    );
  });

  it('queries ECS tasks and services by ARN, other ECS resources by id', () => {
    const arn = 'arn:aws:ecs:us-east-1:000000000000:service/example-cluster/example-service';
    expect(cloudMetricsTarget({ resource_id: 'example-service', arn, type: 'service', service_name: 'AmazonECS' })?.resourceId).toBe(arn);
    expect(cloudMetricsTarget({ resource_id: 'example-cluster', arn, type: 'cluster', service_name: 'AmazonECS' })?.resourceId).toBe(
      'example-cluster'
    );
  });

  it('returns null when the resource row is missing or has no provider id', () => {
    expect(cloudMetricsTarget(null)).toBeNull();
    expect(cloudMetricsTarget({ name: 'example', service_name: 'AmazonEC2' })).toBeNull();
  });
});
