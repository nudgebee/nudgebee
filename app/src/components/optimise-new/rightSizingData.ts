/**
 * Shared parsing + summarisation for K8s pod right-sizing recommendation JSONB.
 *
 * The blob is keyed by container name; each value is an array of per-resource
 * entries: { resource: 'cpu'|'memory', allocated: {request,limit},
 * recommended: {request,limit}, add_info: { cpu_percentile_99, ... } }.
 * (Older shape: a top-level `notifications` array → a single "default" row.)
 *
 * Used by both DetailsPanel (the change table) and buildInterpretation (the
 * verdict / usage strip / reclaimed-amount impact) so the parsing lives in one
 * place. CPU is in cores; memory in bytes.
 */

export interface ContainerEntry {
  containerName: string;
  cpu: any;
  memory: any;
}

export const extractContainerData = (data: any): ContainerEntry[] => {
  if (!data) return [];
  if (data.notifications && Array.isArray(data.notifications)) {
    const cpu = data.notifications.find((n: any) => n.resource === 'cpu');
    const mem = data.notifications.find((n: any) => n.resource === 'memory');
    return [{ containerName: 'default', cpu, memory: mem }];
  }
  const containers: ContainerEntry[] = [];
  for (const [key, value] of Object.entries(data)) {
    if (Array.isArray(value) && value.length > 0 && (value[0] as any)?.resource) {
      const arr = value as any[];
      const cpu = arr.find((v) => v.resource === 'cpu');
      const mem = arr.find((v) => v.resource === 'memory');
      containers.push({ containerName: key, cpu, memory: mem });
    }
  }
  return containers;
};

export const formatCpuShort = (cores: number | null | undefined): string => {
  if (cores == null) return '—';
  if (cores < 1) return `${Math.round(cores * 1000)}m`;
  return `${Number(cores).toFixed(2)}`;
};

// Memory requests are bytes; the >100000 guard tolerates a value already in Mi.
export const formatMemShort = (bytes: number | null | undefined): string => {
  if (bytes == null) return '—';
  const mi = bytes > 100000 ? bytes / (1024 * 1024) : bytes;
  if (mi >= 1024) return `${(mi / 1024).toFixed(1)} Gi`;
  return `${Math.round(mi)} Mi`;
};

export type RightSizingDirection = 'reduce' | 'increase' | 'set' | 'mixed' | 'none';

export interface RightSizingSummary {
  containerCount: number;
  primary: string;
  direction: RightSizingDirection;
  /** Primary container change, e.g. "CPU 20m → 10m · Mem 100 Mi → 68 Mi". */
  changeText: string;
  /** Freed capacity across containers, e.g. "10m vCPU & 32 Mi" (reductions only). */
  reclaimText: string;
  /** Compact usage evidence (primary container) for the strip under "why". */
  evidence: { label: string; value: string }[];
}

const reqOf = (entry: any): number | null => {
  const v = entry?.recommended?.request;
  return typeof v === 'number' ? v : null;
};
const allocOf = (entry: any): number | null => {
  const v = entry?.allocated?.request;
  return typeof v === 'number' ? v : null;
};

export function summarizeRightSizing(recData: any): RightSizingSummary | null {
  const containers = extractContainerData(recData);
  if (containers.length === 0) return null;

  let cpuUp = 0,
    cpuDown = 0,
    memUp = 0,
    memDown = 0,
    unset = 0,
    reclaimedCpu = 0,
    reclaimedMem = 0;

  for (const c of containers) {
    const cc = allocOf(c.cpu);
    const cr = reqOf(c.cpu);
    const mc = allocOf(c.memory);
    const mr = reqOf(c.memory);
    if ((cr != null && cc == null) || (mr != null && mc == null)) unset++;
    if (cc != null && cr != null && cc !== cr) {
      if (cr > cc) cpuUp++;
      else {
        cpuDown++;
        reclaimedCpu += cc - cr;
      }
    }
    if (mc != null && mr != null && mc !== mr) {
      if (mr > mc) memUp++;
      else {
        memDown++;
        reclaimedMem += mc - mr;
      }
    }
  }

  const ups = cpuUp + memUp;
  const downs = cpuDown + memDown;
  let direction: RightSizingDirection;
  if (ups === 0 && downs === 0) direction = unset > 0 ? 'set' : 'none';
  else if (ups > 0 && downs === 0) direction = 'increase';
  else if (downs > 0 && ups === 0) direction = 'reduce';
  else direction = 'mixed';

  const primary = containers[0];
  const cpuCur = allocOf(primary.cpu);
  const cpuRec = reqOf(primary.cpu);
  const memCur = allocOf(primary.memory);
  const memRec = reqOf(primary.memory);

  // Only name a resource in the verdict if it actually changes (or is being set).
  const changeParts: string[] = [];
  if (cpuRec != null && (cpuCur == null || cpuCur !== cpuRec)) {
    changeParts.push(`CPU ${cpuCur != null ? formatCpuShort(cpuCur) : 'unset'} → ${formatCpuShort(cpuRec)}`);
  }
  if (memRec != null && (memCur == null || memCur !== memRec)) {
    changeParts.push(`Mem ${memCur != null ? formatMemShort(memCur) : 'unset'} → ${formatMemShort(memRec)}`);
  }

  const reclaimBits: string[] = [];
  if (reclaimedCpu > 0) reclaimBits.push(`${formatCpuShort(reclaimedCpu)} vCPU`);
  if (reclaimedMem > 0) reclaimBits.push(formatMemShort(reclaimedMem));

  // Observed usage only — the recommended values are already in the verdict and
  // the "Recommended Resource Changes" table below, so we don't repeat them.
  const p99 = primary.cpu?.add_info?.cpu_percentile_99;
  const p95 = primary.cpu?.add_info?.cpu_percentile_95;
  const evidence: { label: string; value: string }[] = [];
  if (typeof p99 === 'number') evidence.push({ label: 'CPU usage P99', value: formatCpuShort(p99) });
  if (typeof p95 === 'number') evidence.push({ label: 'P95', value: formatCpuShort(p95) });

  return {
    containerCount: containers.length,
    primary: primary.containerName,
    direction,
    changeText: changeParts.join(' · '),
    reclaimText: reclaimBits.join(' & '),
    evidence,
  };
}

// ─── Cloud instance right-sizing (aws_ec2_underutilized & friends) ───
//
// Two payload shapes converge here:
//  - underutilized: `recommendedInstances:[{instanceType,price}]`,
//    `recommendedVCpu`, `recommendedMemoryMiB`, `cpu.values[]` (a CPU% series).
//  - alternate/generation: `current_instance_type` → `recommended_instance_type`
//    with `current_price`/`recommended_price`.

export interface CloudRightSizingSummary {
  currentInstance: string;
  targetInstance: string;
  /** $/hr on the recommended type, when known. */
  targetPrice: number | null;
  vcpu: number | null;
  /** Recommended memory in MiB (already MiB, not bytes). */
  memMiB: number | null;
  avgCpuPct: number | null;
  maxCpuPct: number | null;
}

// Coerce stringified numbers too — some JSONB payloads deliver prices/counts as
// strings (matches the `num` helper in buildInterpretation).
const numOf = (v: any): number | null => {
  const n = typeof v === 'string' ? parseFloat(v) : v;
  return typeof n === 'number' && Number.isFinite(n) ? n : null;
};

export function summarizeCloudRightSizing(recData: any): CloudRightSizingSummary | null {
  if (!recData || typeof recData !== 'object' || Array.isArray(recData)) return null;

  const currentInstance = recData.current_instance_type || recData.instance_type || '';
  const recInst = Array.isArray(recData.recommendedInstances) ? recData.recommendedInstances[0] : null;
  const targetInstance = recData.recommended_instance_type || recInst?.instanceType || '';
  const targetPrice = numOf(recInst?.price) ?? numOf(recData.recommended_price);
  const vcpu = numOf(recData.recommendedVCpu);
  const memMiB = numOf(recData.recommendedMemoryMiB);

  const cpuVals = Array.isArray(recData.cpu?.values) ? (recData.cpu.values as any[]).filter((v) => typeof v === 'number') : [];
  const avgCpuPct = cpuVals.length ? cpuVals.reduce((a, b) => a + b, 0) / cpuVals.length : null;
  const maxCpuPct = cpuVals.length ? Math.max(...cpuVals) : null;

  // Nothing usable → let the caller fall back to the catalog title.
  if (!currentInstance && !targetInstance && vcpu == null && memMiB == null && avgCpuPct == null) return null;
  return { currentInstance, targetInstance, targetPrice, vcpu, memMiB, avgCpuPct, maxCpuPct };
}

/** Compact target-spec string, e.g. "1 vCPU · 512 Mi". */
export function formatCloudTargetSpec(s: CloudRightSizingSummary): string {
  return [s.vcpu != null ? `${s.vcpu} vCPU` : '', s.memMiB != null ? formatMemShort(s.memMiB) : ''].filter(Boolean).join(' · ');
}
