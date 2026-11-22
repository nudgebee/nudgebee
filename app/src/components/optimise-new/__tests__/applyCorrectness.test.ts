import { interpolateMitigations, recommendationDetails } from '@api1/recommendation/data';
import { buildCliCommand } from '@components/optimise-new/cliCommands';

// The catalog's mitigations are executed by Apply, not just copied, so a value
// baked into one is a value applied to a real resource that nobody chose.
describe('apply commands carry the values the recommendation computed', () => {
  // The catalog groups rules under Configuration / Security / InfraUpgrade /
  // RightSizing, which is not the tab a rule shows on — look it up by rule.
  // A rule that is absent throws, so a typo cannot pass as "no command"; a rule
  // that is present with no mitigations returns undefined, which two of the
  // cases below assert on.
  type CatalogEntry = { mitigations?: string[] };
  const mitigationsFor = (rule: string): string[] | undefined => {
    const catalog = recommendationDetails as Record<string, Record<string, CatalogEntry>>;
    const group = Object.values(catalog).find((g) => g && rule in g);
    if (!group) throw new Error(`rule ${rule} is not in the catalog`);
    return group[rule]?.mitigations;
  };

  it('sets the retention the finding proposes, not a fixed ninety days', () => {
    const out = interpolateMitigations(mitigationsFor('aws_cloudwatch_log_group_retention'), {
      rule_name: 'aws_cloudwatch_log_group_retention',
      recommendation: { log_group_name: '/aws/lambda/api', proposed_retention_days: 14 },
    });
    expect(out?.[0]).toContain('--retention-in-days 14');
    expect(out?.[0]).not.toContain('--retention-in-days 90');
  });

  it('names the RDS class that was recommended, from either payload shape', () => {
    const alternates = interpolateMitigations(mitigationsFor('aws_rds_alternate_instances'), {
      rule_name: 'aws_rds_alternate_instances',
      resource_name: 'orders-db',
      recommendation: { alternate_instances: [{ product: { attributes: { instanceType: 'db.t4g.micro' } } }] },
    });
    expect(alternates?.[0]).toContain('--db-instance-class db.t4g.micro');

    const recommended = interpolateMitigations(mitigationsFor('aws_rds_underutilized'), {
      rule_name: 'aws_rds_underutilized',
      resource_name: 'orders-db',
      recommendation: { recommendedInstances: [{ product: { attributes: { instanceType: 'db.t4g.small' } } }] },
    });
    expect(recommended?.[0]).toContain('--db-instance-class db.t4g.small');

    // Every rule still carrying the template must be able to fill it.
    for (const rule of ['aws_rds_underutilized', 'aws_rds_overutilized', 'aws_rds_alternate_instances']) {
      const filled = interpolateMitigations(mitigationsFor(rule), {
        rule_name: rule,
        resource_name: 'orders-db',
        recommendation: { recommendedInstances: [{ product: { attributes: { instanceType: 'db.r6g.large' } } }] },
      });
      expect(filled?.[0]).not.toContain('{{RDS_INSTANCE_CLASS}}');
    }
  });

  it('templates the EFS filesystem the finding is about', () => {
    const out = interpolateMitigations(mitigationsFor('aws_efs_lifecycle_policy'), {
      rule_name: 'aws_efs_lifecycle_policy',
      recommendation: { filesystem_id: 'fs-0abc123' },
    });
    expect(out?.[0]).toContain('--file-system-id fs-0abc123');
  });

  it("runs the gp3 upgrade in the volume's own region, the same command Copy Command shows", () => {
    // Without --region the executor falls back to the account's default region,
    // where a volume from any other region is not found.
    const rec = {
      rule_name: 'aws_ec2_ebs_generation_upgrade',
      resource_region: 'eu-west-1',
      recommendation: { volume_id: 'vol-0abc123', volume_region: 'eu-west-1', recommendded_volume_type: 'gp3' },
    };
    const out = interpolateMitigations(mitigationsFor('aws_ec2_ebs_generation_upgrade'), rec);
    const applied = out?.[0]?.match(/```\n?([\s\S]*?)```/)?.[1]?.trim();
    expect(applied).toBe('aws ec2 modify-volume --volume-id vol-0abc123 --volume-type gp3 --region eu-west-1');
    expect(buildCliCommand(rec)?.commands).toEqual([applied]);

    // A payload without volume_region still names the row's region.
    const fromRow = interpolateMitigations(mitigationsFor('aws_ec2_ebs_generation_upgrade'), {
      ...rec,
      recommendation: { volume_id: 'vol-0abc123', recommendded_volume_type: 'gp3' },
    });
    expect(fromRow?.[0]).toContain('--region eu-west-1');
  });

  it('offers no command for the rules that cannot name their own target', () => {
    // An RDS generation finding carries the current class and no replacement, so
    // no template can fill one in; an elastic IP needs a lookup the executor
    // cannot run, since it splits a command and runs the binary directly.
    expect(mitigationsFor('aws_rds_instance_generation')).toBeUndefined();
  });

  it('offers no runnable command for an elastic IP, which needs a lookup first', () => {
    // The executor splits a command and runs the binary directly, so the two-step
    // release cannot run server-side, and the finding carries no allocation id.
    expect(mitigationsFor('aws_vpc_unallocated_elastic_ip')).toBeUndefined();
  });
});
