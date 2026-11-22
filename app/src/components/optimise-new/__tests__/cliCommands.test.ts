import { buildCliCommand, hasCliCommand } from '@components/optimise-new/cliCommands';

// Payloads mirror rows sampled from dev on 2026-09-10; ids are altered.
const rec = (rule_name: string, recommendation: any, extra: Record<string, any> = {}) => ({
  id: 'r1',
  rule_name,
  category: 'RightSizing',
  recommendation,
  ...extra,
});

describe('buildCliCommand', () => {
  it('scales a workload to its recommended replica count', () => {
    const cmd = buildCliCommand(
      rec('replica_right_sizing', {
        kind: 'Deployment',
        metadata: { name: 'vmalert', namespace: 'nudgebee-agent' },
        recommendation: { recommended_replica: 2, allocated_replica: 4 },
      })
    );
    expect(cmd?.commands).toEqual(['kubectl scale deployment/vmalert -n nudgebee-agent --replicas=2']);
    expect(cmd?.destructive).toBe(false);
  });

  it('reads the replica target off the series when there is no scalar', () => {
    const cmd = buildCliCommand(
      rec('replica_right_sizing', {
        kind: 'StatefulSet',
        metadata: { name: 'kafka', namespace: 'streams' },
        recommendation: { recommended: [{ replicas: 3, timestamp: 't' }] },
      })
    );
    expect(cmd?.commands[0]).toBe('kubectl scale statefulset/kafka -n streams --replicas=3');
  });

  it('has no command for a replica row that errored without a target', () => {
    expect(
      hasCliCommand(
        rec('replica_right_sizing', { kind: 'Deployment', metadata: { name: 'x', namespace: 'y' }, recommendation: { error: 'limits not set' } })
      )
    ).toBe(false);
  });

  it('scales an abandoned workload to zero from the resource meta', () => {
    const cmd = buildCliCommand(
      rec(
        'abandoned_resource',
        { window: '7 DAY', traffic: 601 },
        { cloud_resourse: { meta: { controller: 'legacy-api', controllerKind: 'Deployment', namespace: 'apps' } } }
      )
    );
    expect(cmd?.commands).toEqual(['kubectl scale deployment/legacy-api -n apps --replicas=0']);
    expect(cmd?.safeguard).toContain('Scale it back up');
  });

  it('deletes an unused claim and says the data is unrecoverable', () => {
    const cmd = buildCliCommand(
      rec('unused_pvc', {
        kind: 'PersistentVolume',
        spec: { claimRef: { name: 'data-postgresql-0', namespace: 'nudgebee' }, capacity: { storage: '8Gi' } },
      })
    );
    expect(cmd?.commands).toEqual(['kubectl delete pvc data-postgresql-0 -n nudgebee']);
    expect(cmd?.destructive).toBe(true);
    expect(cmd?.safeguard).toContain('cannot be recovered');
  });

  it('snapshots then deletes an orphaned EBS volume in its region', () => {
    const cmd = buildCliCommand(
      rec('aws_ec2_orphaned_volume', { volume_id: 'vol-0abc', volume_region: 'us-east-1', volume_type: 'gp3', cloud_provider: 'aws' })
    );
    expect(cmd?.commands).toEqual([
      "aws ec2 create-snapshot --volume-id vol-0abc --description 'before deleting unattached volume' --region us-east-1",
      'aws ec2 delete-volume --volume-id vol-0abc --region us-east-1',
    ]);
    expect(cmd?.destructive).toBe(true);
  });

  it('skips the cross-cloud orphaned volume rule for a non-AWS row', () => {
    expect(hasCliCommand(rec('orphaned_volume', { volume_id: 'disk-1', cloud_provider: 'azure' }))).toBe(false);
  });

  it('converts a gp2 volume to gp3 in place', () => {
    const cmd = buildCliCommand(rec('aws_ec2_ebs_generation_upgrade', { volume_id: 'vol-0322', volume_region: 'us-east-1' }));
    expect(cmd?.commands).toEqual(['aws ec2 modify-volume --volume-id vol-0322 --volume-type gp3 --region us-east-1']);
    expect(cmd?.destructive).toBe(false);
  });

  it('resizes an EC2 instance with a stop, change, start sequence using the row region', () => {
    const cmd = buildCliCommand(
      rec(
        'aws_ec2_underutilized',
        { instance_id: 'i-0179', recommended_instance_type: 't2.nano', cloud_provider: 'aws' },
        { resource_region: 'eu-west-1' }
      )
    );
    expect(cmd?.commands).toEqual([
      'aws ec2 stop-instances --instance-ids i-0179 --region eu-west-1',
      'aws ec2 wait instance-stopped --instance-ids i-0179 --region eu-west-1',
      'aws ec2 modify-instance-attribute --instance-id i-0179 --instance-type \'{"Value":"t2.nano"}\' --region eu-west-1',
      'aws ec2 start-instances --instance-ids i-0179 --region eu-west-1',
    ]);
  });

  it('falls back to the availability zone on the resource for the region', () => {
    const cmd = buildCliCommand(
      rec(
        'aws_ec2_alternate_instances',
        { instance_id: 'i-1', recommended_instance_type: 'm6g.2xlarge' },
        { cloud_resourse: { meta: { Placement: { AvailabilityZone: 'us-east-1c' } } } }
      )
    );
    expect(cmd?.commands[0]).toBe('aws ec2 stop-instances --instance-ids i-1 --region us-east-1');
  });

  it('releases an elastic IP through its allocation id', () => {
    const cmd = buildCliCommand(
      rec('aws_vpc_unallocated_elastic_ip', { elastic_ip: '52.200.219.207', cloud_provider: 'aws' }, { resource_region: 'us-east-1' })
    );
    expect(cmd?.commands[0]).toContain("describe-addresses --public-ips 52.200.219.207 --query 'Addresses[0].AllocationId'");
    expect(cmd?.commands[1]).toBe('aws ec2 release-address --allocation-id "$ALLOCATION_ID" --region us-east-1');
    expect(cmd?.destructive).toBe(true);
  });

  it('sets a log group retention from the proposed days', () => {
    const cmd = buildCliCommand(
      rec('aws_cloudwatch_log_group_retention', { log_group_name: '/aws/lambda/fn', proposed_retention_days: 30 }, { resource_region: 'us-east-1' })
    );
    expect(cmd?.commands).toEqual(['aws logs put-retention-policy --log-group-name /aws/lambda/fn --retention-in-days 30 --region us-east-1']);
  });

  it('turns on Autoclass for a GCP bucket and skips the Azure rows sharing the rule', () => {
    const gcp = buildCliCommand(
      rec('storage_class_optimization', {
        bucket_name: 'artifacts.example.appspot.com',
        recommended_action: 'enable_autoclass',
        enablement_charge: 0.42,
        cloud_provider: 'gcp',
      })
    );
    expect(gcp?.commands).toEqual(['gcloud storage buckets update gs://artifacts.example.appspot.com --enable-autoclass']);
    expect(gcp?.intro).toContain('One-time enablement charge ≈ $0.42.');
    expect(gcp?.destructive).toBe(false);
    expect(hasCliCommand(rec('storage_class_optimization', { benefits: [], cloud_provider: 'azure' }))).toBe(false);
  });

  it('offers no command for a GCP storage class row from before Autoclass pricing', () => {
    const legacy = rec('storage_class_optimization', {
      bucket_name: 'artifacts.example.appspot.com',
      recommended_classes: ['NEARLINE', 'COLDLINE', 'ARCHIVE'],
      cloud_provider: 'gcp',
    });
    expect(hasCliCommand(legacy)).toBe(false);
  });

  it('quotes shell metacharacters in names', () => {
    const cmd = buildCliCommand(rec('unused_pvc', { spec: { claimRef: { name: "it's-a-claim", namespace: 'ns' } } }));
    expect(cmd?.commands[0]).toBe("kubectl delete pvc 'it'\\''s-a-claim' -n ns");
    expect(buildCliCommand(rec('unused_pvc', { spec: { claimRef: { name: 'has space', namespace: 'ns' } } }))?.commands[0]).toBe(
      "kubectl delete pvc 'has space' -n ns"
    );
  });

  it('keeps the existing kubectl patch for pod rightsizing and nothing for unknown rules', () => {
    expect(buildCliCommand(rec('pod_right_sizing', {}, { resource_name: 'api' }))?.commands[0]).toContain('# No resource changes recommended');
    expect(hasCliCommand(rec('aws_rds_alternate_instances', { alternate_instances: [] }))).toBe(false);
    expect(hasCliCommand(rec('pv_rightsize', { spec: { claimRef: { name: 'a', namespace: 'b' } }, recommendation: { recommend_size: 3 } }))).toBe(
      false
    );
    expect(hasCliCommand({ rule_name: 'unused_pvc', recommendation: 'not json' })).toBe(false);
  });
});
