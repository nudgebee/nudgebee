// Derives provisioned compute capacity (vCPU + memory) for a database instance
// row from the provider metadata already stored in cloud_resourses.meta.
//
// Correctness contract: values are either read directly from provider data
// (AWS pricing attributes, Azure sku.capacity) or derived from specs the
// provider publishes for the machine type (GCP tier string encoding, Azure
// GB-per-vCore hardware ratios). Anything we cannot derive exactly returns
// null and renders as '-' — never an approximation. Azure DTU SKUs are shown
// as DTUs because Microsoft publishes no exact vCPU/memory mapping for them.

export interface InstanceCompute {
  cpu: string | null;
  memory: string | null;
}

const NO_COMPUTE: InstanceCompute = { cpu: null, memory: null };

const formatGb = (gb: number): string => `${Number(gb.toFixed(2))} GB`;

// GCP legacy shared-core tiers ship a fixed spec (cloud.google.com/sql/docs/mysql/instance-settings).
const GCP_SHARED_CORE_TIERS: Record<string, InstanceCompute> = {
  'db-f1-micro': { cpu: '1 shared vCPU', memory: '0.6 GB' },
  'db-g1-small': { cpu: '1 shared vCPU', memory: '1.7 GB' },
};

// Enterprise Plus (db-perf-optimized-N-*) memory is per-size, not a uniform
// ratio — 128 vCPU caps at 864 GB, breaking the 8 GB/vCPU pattern.
const GCP_PERF_OPTIMIZED_MEMORY_GB: Record<number, number> = {
  2: 16,
  4: 32,
  8: 64,
  16: 128,
  32: 256,
  48: 384,
  64: 512,
  80: 640,
  96: 768,
  128: 864,
};

export const parseGcpTier = (tier: string): InstanceCompute => {
  const shared = GCP_SHARED_CORE_TIERS[tier];
  if (shared) return shared;

  const custom = tier.match(/^db-custom-(\d+)-(\d+)$/);
  if (custom) {
    return { cpu: `${Number(custom[1])} vCPU`, memory: formatGb(Number(custom[2]) / 1024) };
  }

  const perfOptimized = tier.match(/^db-perf-optimized-N-(\d+)$/);
  if (perfOptimized) {
    const vcpu = Number(perfOptimized[1]);
    const memoryGb = GCP_PERF_OPTIMIZED_MEMORY_GB[vcpu];
    return { cpu: `${vcpu} vCPU`, memory: memoryGb ? formatGb(memoryGb) : null };
  }

  const n1 = tier.match(/^db-n1-(standard|highmem)-(\d+)$/);
  if (n1) {
    const vcpu = Number(n1[2]);
    const gbPerVcpu = n1[1] === 'standard' ? 3.75 : 6.5;
    return { cpu: `${vcpu} vCPU`, memory: formatGb(vcpu * gbPerVcpu) };
  }

  return NO_COMPUTE;
};

const AZURE_DTU_TIERS = ['basic', 'standard', 'premium'];
const AZURE_VCORE_TIERS = ['generalpurpose', 'businesscritical', 'hyperscale'];

// GB of memory per vCore by hardware generation (learn.microsoft.com,
// "resource limits" pages for vCore-based SQL Database).
const AZURE_GB_PER_VCORE: Record<string, number> = {
  gen4: 7,
  gen5: 5.1,
  fsv2: 1.89,
  dc: 4.5,
  m: 29.4,
  // Newer API family names: standard-series is renamed Gen5 (same ratio),
  // premium-series ratios are published on the same resource-limits pages.
  standard_series: 5.1,
  premium_series: 5.1,
  premium_series_memory_optimized: 10.2,
};

export const parseAzureSku = (sku: { name?: string; tier?: string; family?: string; capacity?: number }): InstanceCompute => {
  const tier = sku.tier?.toLowerCase() || '';
  const capacity = sku.capacity;
  if (!capacity) return NO_COMPUTE;

  if (AZURE_DTU_TIERS.includes(tier)) {
    return { cpu: `${capacity} DTU`, memory: null };
  }

  if (AZURE_VCORE_TIERS.includes(tier)) {
    // Serverless (…_S_…) capacity is the max-vCore bound and its memory limit
    // follows a different serverless-specific curve — report vCores only.
    const isServerless = /_S_/.test(sku.name || '');
    const cpu = `${capacity} vCore${isServerless ? ' (max)' : ''}`;
    const gbPerVcore = AZURE_GB_PER_VCORE[sku.family?.toLowerCase() || ''];
    const memory = !isServerless && gbPerVcore ? formatGb(capacity * gbPerVcore) : null;
    return { cpu, memory };
  }

  return NO_COMPUTE;
};

export const getInstanceCompute = (item: any): InstanceCompute => {
  const meta = item?.meta;
  if (!meta) return NO_COMPUTE;

  if (meta.DBInstanceClass) {
    if (meta.DBInstanceClass === 'db.serverless') {
      return { cpu: 'Serverless', memory: null };
    }
    const attributes = meta.InstanceTypeDetails?.product?.attributes;
    return {
      cpu: attributes?.vcpu ? `${attributes.vcpu} vCPU` : null,
      memory: attributes?.memory || null,
    };
  }

  if (typeof meta.settings?.tier === 'string') {
    return parseGcpTier(meta.settings.tier);
  }

  if (meta.sku) {
    return parseAzureSku(meta.sku);
  }

  return NO_COMPUTE;
};
