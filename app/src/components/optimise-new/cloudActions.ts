import { safeParseJSON } from './utils';

// Cloud fixes NudgeBee applies through the cloud account itself, with no
// cluster agent involved. The backend applies only the stored recommendation,
// so the confirm surface sends no data of its own.
export type CloudActionRule = 'aws_ec2_ebs_generation_upgrade';

export interface CloudActionFact {
  label: string;
  value: string;
  copyable?: boolean;
}

export interface CloudAction {
  rule: CloudActionRule;
  /** Button label on the action bar and the modal title. */
  label: string;
  /** The confirm button's verb. */
  confirmText: string;
  /** One line above the facts saying what will happen. */
  summary: string;
  facts: CloudActionFact[];
  /** What applying does, from the recommendation itself, when it says. */
  impact?: string;
  /** Toast once the change has started. */
  successMessage: string;
}

const str = (value: unknown): string => (value == null ? '' : String(value));

const positive = (value: unknown): number | null => {
  const n = Number(value);
  return Number.isFinite(n) && n > 0 ? n : null;
};

// cloudActionFor describes the rule's action whatever the recommendation's
// status, so a recommendation of this rule always reaches the cloud confirm
// surface; canStartCloudAction decides whether it may be started.
export const cloudActionFor = (rec: any): CloudAction | null => {
  if (!rec) return null;
  const payload = safeParseJSON(rec.recommendation) || {};

  switch (rec.rule_name as CloudActionRule) {
    case 'aws_ec2_ebs_generation_upgrade': {
      const volumeId = str(payload.volume_id);
      if (!volumeId) return null;
      const size = positive(payload.volume_size);
      const iops = positive(payload.recommended_iops);
      const throughput = positive(payload.recommended_throughput);
      const savings = Math.round(Number(rec.estimated_savings) || 0);
      return {
        rule: 'aws_ec2_ebs_generation_upgrade',
        label: 'Upgrade to gp3',
        confirmText: 'Upgrade to gp3',
        summary: 'Converts this volume from gp2 to gp3 in place — no downtime, and the data stays on the volume.',
        facts: [
          { label: 'Volume', value: volumeId, copyable: true },
          ...(payload.volume_region ? [{ label: 'Region', value: str(payload.volume_region) }] : []),
          ...(size ? [{ label: 'Size', value: `${size} GiB` }] : []),
          { label: 'Type', value: 'gp2 → gp3' },
          ...(iops ? [{ label: 'IOPS', value: String(iops) }] : []),
          ...(throughput ? [{ label: 'Throughput', value: `${throughput} MiB/s` }] : []),
          ...(savings >= 1 ? [{ label: 'Est. saving', value: `~$${savings}/month` }] : []),
        ],
        impact: payload.apply_impact ? str(payload.apply_impact) : undefined,
        successMessage: `Upgrade of ${volumeId} to gp3 started — it runs in the background; follow it under Resolutions.`,
      };
    }
    default:
      return null;
  }
};

// Only an Open recommendation can be started: once applied it moves to
// InProgress while the change runs. A row loaded without its status is not
// assumed Open.
export const canStartCloudAction = (rec: any): boolean => rec?.status === 'Open' && cloudActionFor(rec) != null;
