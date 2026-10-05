import { canStartCloudAction, cloudActionFor } from '../cloudActions';

const ebsRec = {
  id: 'rec-ebs',
  account_id: 'acct-1',
  category: 'InfraUpgrade',
  rule_name: 'aws_ec2_ebs_generation_upgrade',
  status: 'Open',
  estimated_savings: 12.4,
  recommendation: JSON.stringify({
    volume_id: 'vol-0abc',
    volume_region: 'us-east-1',
    volume_size: 500,
    recommended_iops: 4000,
    recommended_throughput: 250,
    apply_impact: 'Converts this volume to gp3 in place.',
  }),
};

describe('cloudActionFor', () => {
  it('describes the gp3 upgrade from the recommendation payload', () => {
    const action = cloudActionFor(ebsRec);
    expect(action).toMatchObject({
      rule: 'aws_ec2_ebs_generation_upgrade',
      label: 'Upgrade to gp3',
      impact: 'Converts this volume to gp3 in place.',
    });
    expect(action?.facts).toEqual([
      { label: 'Volume', value: 'vol-0abc', copyable: true },
      { label: 'Region', value: 'us-east-1' },
      { label: 'Size', value: '500 GiB' },
      { label: 'Type', value: 'gp2 → gp3' },
      { label: 'IOPS', value: '4000' },
      { label: 'Throughput', value: '250 MiB/s' },
      { label: 'Est. saving', value: '~$12/month' },
    ]);
  });

  it('leaves out performance and saving facts the recommendation does not carry', () => {
    const action = cloudActionFor({ ...ebsRec, estimated_savings: 0.3, recommendation: { volume_id: 'vol-small', volume_size: 100 } });
    expect(action?.facts.map((f) => f.label)).toEqual(['Volume', 'Size', 'Type']);
    expect(action?.impact).toBeUndefined();
  });

  it('describes the rule whatever the status, so it never routes to another confirm surface', () => {
    expect(cloudActionFor({ ...ebsRec, status: 'InProgress' })).not.toBeNull();
    expect(cloudActionFor({ ...ebsRec, status: undefined })).not.toBeNull();
  });

  it('can be started only when the recommendation is known to be open', () => {
    expect(canStartCloudAction(ebsRec)).toBe(true);
    expect(canStartCloudAction({ ...ebsRec, status: 'InProgress' })).toBe(false);
    expect(canStartCloudAction({ ...ebsRec, status: 'Closed' })).toBe(false);
    expect(canStartCloudAction({ ...ebsRec, status: undefined })).toBe(false);
  });

  it('is not offered without a volume id or for other cloud rules', () => {
    expect(cloudActionFor({ ...ebsRec, recommendation: {} })).toBeNull();
    expect(cloudActionFor({ ...ebsRec, rule_name: 'azure_disk_unattached_volume' })).toBeNull();
    expect(cloudActionFor({ ...ebsRec, rule_name: 'gcp_compute_stopped_instance' })).toBeNull();
  });
});
