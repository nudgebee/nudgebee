import { sameProviderAccountIds } from '../optimizationRules';

const accounts = [
  { value: 'aws-2', cloud_provider: 'AWS' },
  { value: 'gcp-1', cloud_provider: 'GCP' },
  { value: 'aws-1', cloud_provider: 'AWS' },
  { value: 'k8s-1', cloud_provider: 'K8s' },
  { value: 'no-provider', cloud_provider: '' },
];

// Regression guard for #39837: the Optimization trigger offered GCP rules for an AWS account.
describe('sameProviderAccountIds', () => {
  it('returns every account of the picked account’s provider and none of another', () => {
    expect(sameProviderAccountIds('aws-1', accounts)).toEqual(['aws-1', 'aws-2']);
    expect(sameProviderAccountIds('gcp-1', accounts)).toEqual(['gcp-1']);
  });

  it('keeps a Kubernetes cluster to Kubernetes accounts', () => {
    expect(sameProviderAccountIds('k8s-1', accounts)).toEqual(['k8s-1']);
  });

  it('does not narrow when nothing is picked or the pick cannot be resolved', () => {
    expect(sameProviderAccountIds('', accounts)).toEqual([]);
    expect(sameProviderAccountIds('deleted-account', accounts)).toEqual([]);
    expect(sameProviderAccountIds('no-provider', accounts)).toEqual([]);
    expect(sameProviderAccountIds('aws-1', [])).toEqual([]);
  });
});
