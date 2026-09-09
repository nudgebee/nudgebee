import { specificTypeLabel } from '@components/k8s/details/KnowledgeGraphMap';

// The dependency graph draws a workload and the K8sService in front of it as two
// nodes sharing a name and a namespace. The badge cannot separate them (both K8s
// types, and it cannot tell a Deployment from a StatefulSet either), so the
// specific type is what makes them readable.
describe('specificTypeLabel', () => {
  it('names the Kubernetes types a badge cannot distinguish', () => {
    expect(specificTypeLabel('KubernetesDeployment', 'Workload')).toBe('Deployment');
    expect(specificTypeLabel('KubernetesStatefulSet', 'Workload')).toBe('StatefulSet');
    expect(specificTypeLabel('KubernetesDaemonSet', 'Workload')).toBe('DaemonSet');
    expect(specificTypeLabel('KubernetesJob', 'Workload')).toBe('Job');
    expect(specificTypeLabel('KubernetesCronJob', 'Workload')).toBe('CronJob');
    expect(specificTypeLabel('KubernetesService', 'K8sService')).toBe('Service');
  });

  it('separates a workload from the service in front of it', () => {
    const workload = specificTypeLabel('KubernetesDeployment', 'Workload');
    const service = specificTypeLabel('KubernetesService', 'K8sService');
    expect(workload).not.toBe(service);
  });

  it('adds nothing when specific_type only repeats node_type', () => {
    expect(specificTypeLabel('Service', 'Service')).toBe('');
    expect(specificTypeLabel('Storage', 'Storage')).toBe('');
    expect(specificTypeLabel('ExternalService', 'ExternalService')).toBe('');
    expect(specificTypeLabel('Database', 'Database')).toBe('');
    expect(specificTypeLabel('MessageQueue', 'MessageQueue')).toBe('');
  });

  it('falls back to the raw specific_type for unmapped cloud types', () => {
    expect(specificTypeLabel('EC2Instance', 'ComputeInstance')).toBe('EC2Instance');
    expect(specificTypeLabel('RDSInstance', 'Database')).toBe('RDSInstance');
    expect(specificTypeLabel('S3Bucket', 'Storage')).toBe('S3Bucket');
    expect(specificTypeLabel('LambdaFunction', 'ServerlessFunction')).toBe('LambdaFunction');
  });

  it('renders nothing when the node carries no specific_type', () => {
    expect(specificTypeLabel(undefined, 'Workload')).toBe('');
    expect(specificTypeLabel('', 'Workload')).toBe('');
    expect(specificTypeLabel(null, 'Workload')).toBe('');
  });
});
