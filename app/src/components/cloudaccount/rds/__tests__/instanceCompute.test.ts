import { getInstanceCompute, parseGcpTier, parseAzureSku } from '../instanceCompute';

describe('parseGcpTier', () => {
  it('parses db-custom tiers (vCPU count + memory MB encoded in the string)', () => {
    expect(parseGcpTier('db-custom-2-8192')).toEqual({ cpu: '2 vCPU', memory: '8 GB' });
    expect(parseGcpTier('db-custom-4-16384')).toEqual({ cpu: '4 vCPU', memory: '16 GB' });
    expect(parseGcpTier('db-custom-1-3840')).toEqual({ cpu: '1 vCPU', memory: '3.75 GB' });
  });

  it('maps legacy shared-core tiers from their published specs', () => {
    expect(parseGcpTier('db-f1-micro')).toEqual({ cpu: '1 shared vCPU', memory: '0.6 GB' });
    expect(parseGcpTier('db-g1-small')).toEqual({ cpu: '1 shared vCPU', memory: '1.7 GB' });
  });

  it('derives legacy n1 tiers from the published GB-per-vCPU ratio', () => {
    expect(parseGcpTier('db-n1-standard-4')).toEqual({ cpu: '4 vCPU', memory: '15 GB' });
    expect(parseGcpTier('db-n1-standard-96')).toEqual({ cpu: '96 vCPU', memory: '360 GB' });
    expect(parseGcpTier('db-n1-highmem-8')).toEqual({ cpu: '8 vCPU', memory: '52 GB' });
  });

  it('maps Enterprise Plus perf-optimized tiers per-size (128 breaks the 8 GB/vCPU pattern)', () => {
    expect(parseGcpTier('db-perf-optimized-N-2')).toEqual({ cpu: '2 vCPU', memory: '16 GB' });
    expect(parseGcpTier('db-perf-optimized-N-128')).toEqual({ cpu: '128 vCPU', memory: '864 GB' });
  });

  it('returns nulls for unknown tiers instead of guessing', () => {
    expect(parseGcpTier('db-perf-optimized-N-3')).toEqual({ cpu: '3 vCPU', memory: null });
    expect(parseGcpTier('db-future-series-4')).toEqual({ cpu: null, memory: null });
    expect(parseGcpTier('')).toEqual({ cpu: null, memory: null });
  });
});

describe('parseAzureSku', () => {
  it('reports DTU count for DTU-model SKUs (no exact vCPU/memory mapping exists)', () => {
    expect(parseAzureSku({ name: 'S0', tier: 'Standard', capacity: 10 })).toEqual({ cpu: '10 DTU', memory: null });
    expect(parseAzureSku({ name: 'P1', tier: 'Premium', capacity: 125 })).toEqual({ cpu: '125 DTU', memory: null });
    expect(parseAzureSku({ name: 'Basic', tier: 'Basic', capacity: 5 })).toEqual({ cpu: '5 DTU', memory: null });
  });

  it('reports vCores + derived memory for vCore SKUs with a known hardware family', () => {
    expect(parseAzureSku({ name: 'GP_Gen5_2', tier: 'GeneralPurpose', family: 'Gen5', capacity: 2 })).toEqual({
      cpu: '2 vCore',
      memory: '10.2 GB',
    });
    expect(parseAzureSku({ name: 'BC_Gen5_8', tier: 'BusinessCritical', family: 'Gen5', capacity: 8 })).toEqual({
      cpu: '8 vCore',
      memory: '40.8 GB',
    });
    expect(parseAzureSku({ name: 'HS_Gen5_4', tier: 'Hyperscale', family: 'Gen5', capacity: 4 })).toEqual({
      cpu: '4 vCore',
      memory: '20.4 GB',
    });
  });

  it('derives memory for the newer series-style hardware family names', () => {
    expect(parseAzureSku({ name: 'GP_Gen5_2', tier: 'GeneralPurpose', family: 'standard_series', capacity: 2 })).toEqual({
      cpu: '2 vCore',
      memory: '10.2 GB',
    });
    expect(parseAzureSku({ name: 'HS_PRMS_4', tier: 'Hyperscale', family: 'premium_series', capacity: 4 })).toEqual({
      cpu: '4 vCore',
      memory: '20.4 GB',
    });
    expect(parseAzureSku({ name: 'HS_MOPRMS_4', tier: 'Hyperscale', family: 'premium_series_memory_optimized', capacity: 4 })).toEqual({
      cpu: '4 vCore',
      memory: '40.8 GB',
    });
  });

  it('reports vCores only for unknown hardware families and serverless SKUs', () => {
    expect(parseAzureSku({ name: 'GP_FutureGen_4', tier: 'GeneralPurpose', family: 'FutureGen', capacity: 4 })).toEqual({
      cpu: '4 vCore',
      memory: null,
    });
    expect(parseAzureSku({ name: 'GP_S_Gen5_2', tier: 'GeneralPurpose', family: 'Gen5', capacity: 2 })).toEqual({
      cpu: '2 vCore (max)',
      memory: null,
    });
  });

  it('returns nulls when capacity or tier is missing/unrecognized', () => {
    expect(parseAzureSku({ name: 'GP_Gen5_2', tier: 'GeneralPurpose' })).toEqual({ cpu: null, memory: null });
    expect(parseAzureSku({ name: 'ElasticPool', tier: 'SomethingElse', capacity: 2 })).toEqual({ cpu: null, memory: null });
  });
});

describe('getInstanceCompute (provider routing)', () => {
  it('reads AWS vcpu/memory from the pricing attributes already stored in meta', () => {
    const item = {
      meta: {
        DBInstanceClass: 'db.r5.large',
        InstanceTypeDetails: { product: { attributes: { vcpu: '2', memory: '16 GiB' } } },
      },
    };
    expect(getInstanceCompute(item)).toEqual({ cpu: '2 vCPU', memory: '16 GiB' });
  });

  it('shows AWS Aurora Serverless v2 as Serverless (ACU-based, no fixed size)', () => {
    expect(getInstanceCompute({ meta: { DBInstanceClass: 'db.serverless' } })).toEqual({ cpu: 'Serverless', memory: null });
  });

  it('returns nulls when the AWS pricing lookup produced no attributes', () => {
    expect(getInstanceCompute({ meta: { DBInstanceClass: 'db.r5.large', InstanceTypeDetails: {} } })).toEqual({ cpu: null, memory: null });
  });

  it('routes GCP rows via settings.tier and Azure rows via sku', () => {
    expect(getInstanceCompute({ meta: { settings: { tier: 'db-custom-2-8192' } } })).toEqual({ cpu: '2 vCPU', memory: '8 GB' });
    expect(getInstanceCompute({ meta: { sku: { name: 'GP_Gen5_2', tier: 'GeneralPurpose', family: 'Gen5', capacity: 2 } } })).toEqual({
      cpu: '2 vCore',
      memory: '10.2 GB',
    });
  });

  it('returns nulls for rows with no meta', () => {
    expect(getInstanceCompute({})).toEqual({ cpu: null, memory: null });
    expect(getInstanceCompute({ meta: {} })).toEqual({ cpu: null, memory: null });
  });
});
