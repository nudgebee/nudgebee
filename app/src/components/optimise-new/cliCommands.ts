/**
 * The CLI command behind a recommendation, for the Copy Command action.
 *
 * Only rules whose action is fully determined by the row and its payload get
 * a command; anything that needs a judgement call (which alternative RDS
 * class, how to shrink a volume) returns null and the action stays hidden.
 * A destructive command is still offered, with one factual line and the
 * safeguard to take first — the gate is confidence to act, not a warning.
 */
import { buildKubectlCommand, safeParseJSON } from './utils';

export interface CliCommand {
  title: string;
  /** One sentence saying what the command does to which resource. */
  intro: string;
  commands: string[];
  /** Permanently removes something; the modal states that and the safeguard. */
  destructive: boolean;
  safeguard?: string;
}

// Bare when it is a plain token, single-quoted otherwise, so the value is safe
// to paste on its own wherever a builder drops it into a command.
const sh = (v: any): string => {
  const s = String(v ?? '');
  return /^[A-Za-z0-9._/:@-]+$/.test(s) ? s : `'${s.replace(/'/g, "'\\''")}'`;
};

const regionOf = (rec: any, payload: any): string => {
  const az = rec?.cloud_resourse?.meta?.Placement?.AvailabilityZone || rec?.resource_meta?.Placement?.AvailabilityZone;
  return payload?.volume_region || payload?.region || rec?.resource_region || (az ? String(az).replace(/[a-z]$/, '') : '');
};

const regionFlag = (region: string): string => (region ? ` --region ${sh(region)}` : '');

const workloadOf = (rec: any, payload: any) => {
  const meta = rec?.cloud_resourse?.meta || rec?.resource_meta || {};
  const kind = payload?.kind || meta.controllerKind || meta.config?.controllerKind || rec?.resource_type || '';
  const name = payload?.metadata?.name || meta.controller || meta.config?.controller || rec?.resource_name || '';
  const namespace = payload?.metadata?.namespace || meta.namespace || meta.config?.namespace || rec?.resource_k8s_namespace || '';
  return { kind: String(kind).toLowerCase(), name, namespace };
};

const scale = (kind: string, name: string, namespace: string, replicas: number) =>
  `kubectl scale ${sh(kind)}/${sh(name)} -n ${sh(namespace)} --replicas=${replicas}`;

const recommendedReplicas = (payload: any): number | null => {
  const rec = payload?.recommendation;
  if (typeof rec?.recommended_replica === 'number') return Math.round(rec.recommended_replica);
  const first = Array.isArray(rec?.recommended) ? rec.recommended[0] : null;
  return typeof first?.replicas === 'number' ? Math.round(first.replicas) : null;
};

type Builder = (rec: any, payload: any) => CliCommand | null;

const ec2Resize: Builder = (rec, payload) => {
  const id = payload?.instance_id;
  const target = payload?.recommended_instance_type;
  if (!id || !target) return null;
  const r = regionFlag(regionOf(rec, payload));
  return {
    title: 'aws cli commands',
    intro: `Stops ${id}, changes it to ${target} and starts it again; the instance is down while it changes.`,
    commands: [
      `aws ec2 stop-instances --instance-ids ${sh(id)}${r}`,
      `aws ec2 wait instance-stopped --instance-ids ${sh(id)}${r}`,
      `aws ec2 modify-instance-attribute --instance-id ${sh(id)} --instance-type ${sh(JSON.stringify({ Value: target }))}${r}`,
      `aws ec2 start-instances --instance-ids ${sh(id)}${r}`,
    ],
    destructive: false,
  };
};

const ebsDelete: Builder = (rec, payload) => {
  const id = payload?.volume_id;
  if (!id || (payload?.cloud_provider && payload.cloud_provider !== 'aws')) return null;
  const r = regionFlag(regionOf(rec, payload));
  return {
    title: 'aws cli commands',
    intro: `Snapshots ${id}, then deletes the volume.`,
    commands: [
      `aws ec2 create-snapshot --volume-id ${sh(id)} --description 'before deleting unattached volume'${r}`,
      `aws ec2 delete-volume --volume-id ${sh(id)}${r}`,
    ],
    destructive: true,
    safeguard: 'Data on a deleted volume cannot be recovered; the first command keeps a snapshot.',
  };
};

const BUILDERS: Record<string, Builder> = {
  pod_right_sizing: (rec) => ({
    title: 'kubectl command',
    intro: `Applies the recommended requests and limits to ${rec?.resource_name || 'the workload'}.`,
    commands: [buildKubectlCommand(rec)],
    destructive: false,
  }),

  replica_right_sizing: (rec, payload) => {
    const { kind, name, namespace } = workloadOf(rec, payload);
    const replicas = recommendedReplicas(payload);
    if (!kind || !name || !namespace || replicas == null) return null;
    return {
      title: 'kubectl command',
      intro: `Scales ${name} to ${replicas} ${replicas === 1 ? 'replica' : 'replicas'}.`,
      commands: [scale(kind, name, namespace, replicas)],
      destructive: false,
    };
  },

  abandoned_resource: (rec, payload) => {
    const { kind, name, namespace } = workloadOf(rec, payload);
    if (!kind || !name || !namespace) return null;
    return {
      title: 'kubectl command',
      intro: `Scales ${name} to zero; the workload stops serving and keeps its definition.`,
      commands: [scale(kind, name, namespace, 0)],
      destructive: false,
      safeguard: 'Scale it back up to restore it.',
    };
  },

  unused_pvc: (rec, payload) => {
    const claim = payload?.spec?.claimRef?.name;
    const namespace = payload?.spec?.claimRef?.namespace || rec?.resource_k8s_namespace;
    if (!claim || !namespace) return null;
    return {
      title: 'kubectl command',
      intro: `Deletes the claim ${claim}; its volume then follows the reclaim policy.`,
      commands: [`kubectl delete pvc ${sh(claim)} -n ${sh(namespace)}`],
      destructive: true,
      safeguard: 'Data on a reclaimed volume cannot be recovered; snapshot the volume first if anything on it matters.',
    };
  },

  aws_ec2_orphaned_volume: ebsDelete,
  orphaned_volume: ebsDelete,

  aws_ec2_ebs_generation_upgrade: (rec, payload) => {
    const id = payload?.volume_id;
    if (!id) return null;
    return {
      title: 'aws cli command',
      intro: `Changes ${id} to gp3 in place; the volume stays in use throughout.`,
      commands: [`aws ec2 modify-volume --volume-id ${sh(id)} --volume-type gp3${regionFlag(regionOf(rec, payload))}`],
      destructive: false,
    };
  },

  aws_ec2_underutilized: ec2Resize,
  aws_ec2_alternate_instances: ec2Resize,
  aws_ec2_instance_generation_upgrade: ec2Resize,

  aws_vpc_unallocated_elastic_ip: (rec, payload) => {
    const ip = payload?.elastic_ip;
    if (!ip) return null;
    const r = regionFlag(regionOf(rec, payload));
    return {
      title: 'aws cli commands',
      intro: `Looks up the allocation behind ${ip} and releases it.`,
      commands: [
        `ALLOCATION_ID=$(aws ec2 describe-addresses --public-ips ${sh(ip)} --query 'Addresses[0].AllocationId' --output text${r})`,
        `aws ec2 release-address --allocation-id "$ALLOCATION_ID"${r}`,
      ],
      destructive: true,
      safeguard: 'A released address returns to the pool and cannot be reclaimed; anything still pointing at it loses connectivity.',
    };
  },

  aws_cloudwatch_log_group_retention: (rec, payload) => {
    const group = payload?.log_group_name;
    const days = payload?.proposed_retention_days;
    if (!group || typeof days !== 'number') return null;
    return {
      title: 'aws cli command',
      intro: `Sets ${group} to keep ${days} days of logs; older events are removed on the next cycle.`,
      commands: [`aws logs put-retention-policy --log-group-name ${sh(group)} --retention-in-days ${days}${regionFlag(regionOf(rec, payload))}`],
      destructive: false,
    };
  },

  storage_class_optimization: (rec, payload) => {
    const bucket = payload?.bucket_name || payload?.bucket_id;
    if (payload?.cloud_provider !== 'gcp' || !bucket || payload?.recommended_action !== 'enable_autoclass') return null;
    const charge = typeof payload?.enablement_charge === 'number' ? ` One-time enablement charge ≈ $${payload.enablement_charge.toFixed(2)}.` : '';
    return {
      title: 'gcloud command',
      intro: `Turns on Autoclass for ${bucket}. Objects not read for 30 days move to Nearline; objects that are read return to Standard.${charge}`,
      commands: [`gcloud storage buckets update ${sh(`gs://${bucket}`)} --enable-autoclass`],
      destructive: false,
    };
  },
};
BUILDERS.gcp_storage_class_optimization = BUILDERS.storage_class_optimization;

/** The rules the Copy Command action can serve. */
export const CLI_COMMAND_RULES = Object.keys(BUILDERS);

export const buildCliCommand = (rec: any): CliCommand | null => {
  const builder = rec?.rule_name ? BUILDERS[rec.rule_name] : undefined;
  if (!builder) return null;
  const payload = safeParseJSON(rec.recommendation);
  try {
    return builder(rec, payload && typeof payload === 'object' ? payload : {});
  } catch {
    return null;
  }
};

export const hasCliCommand = (rec: any): boolean => buildCliCommand(rec) != null;
