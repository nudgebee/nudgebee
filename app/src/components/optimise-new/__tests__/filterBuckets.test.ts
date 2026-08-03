import { SAVINGS_BUCKETS, savingsBucketToParams, LAST_SEEN_BUCKETS, lastSeenBucketToParams } from '../utils';
import { applyFacetFilters } from '@api1/recommendation';

describe('savingsBucketToParams', () => {
  it('returns no params for the Any bucket', () => {
    expect(savingsBucketToParams('')).toEqual({});
  });

  it('maps threshold buckets to savingsGte', () => {
    expect(savingsBucketToParams('gte1')).toEqual({ savingsGte: 1 });
    expect(savingsBucketToParams('gte5')).toEqual({ savingsGte: 5 });
    expect(savingsBucketToParams('gte10')).toEqual({ savingsGte: 10 });
    expect(savingsBucketToParams('gte25')).toEqual({ savingsGte: 25 });
  });

  it('maps the cost-increase bucket to savingsLt 0', () => {
    expect(savingsBucketToParams('cost-increase')).toEqual({ savingsLt: 0 });
  });

  it('returns no params for an unknown key', () => {
    expect(savingsBucketToParams('nope')).toEqual({});
  });

  it('has a label for every bucket', () => {
    SAVINGS_BUCKETS.forEach((b) => expect(b.label).toBeTruthy());
  });
});

describe('lastSeenBucketToParams', () => {
  const NOW = Date.UTC(2026, 7, 3, 12, 0, 0); // 2026-08-03T12:00:00Z

  it('returns no params for the Any-time bucket', () => {
    expect(lastSeenBucketToParams('', NOW)).toEqual({});
  });

  it('maps recency buckets to an updatedAtGte cutoff', () => {
    expect(lastSeenBucketToParams('1h', NOW)).toEqual({ updatedAtGte: '2026-08-03T11:00:00.000Z' });
    expect(lastSeenBucketToParams('24h', NOW)).toEqual({ updatedAtGte: '2026-08-02T12:00:00.000Z' });
    expect(lastSeenBucketToParams('7d', NOW)).toEqual({ updatedAtGte: '2026-07-27T12:00:00.000Z' });
    expect(lastSeenBucketToParams('30d', NOW)).toEqual({ updatedAtGte: '2026-07-04T12:00:00.000Z' });
  });

  it('maps the stale bucket to an updatedAtLt cutoff', () => {
    expect(lastSeenBucketToParams('stale', NOW)).toEqual({ updatedAtLt: '2026-07-04T12:00:00.000Z' });
  });

  it('returns no params for an unknown key', () => {
    expect(lastSeenBucketToParams('nope', NOW)).toEqual({});
  });

  it('has a label for every bucket', () => {
    LAST_SEEN_BUCKETS.forEach((b) => expect(b.label).toBeTruthy());
  });
});

describe('applyFacetFilters', () => {
  it('adds nothing when no facet filters are set', () => {
    const where: any = {};
    applyFacetFilters(where, {});
    expect(where).toEqual({});
  });

  it('filters safety bands with _in', () => {
    const where: any = {};
    applyFacetFilters(where, { safetyBand: ['safe', 'review'] });
    expect(where).toEqual({ safety_band: { _in: ['safe', 'review'] } });
  });

  it('also matches NULL safety_band when unknown is selected', () => {
    const where: any = {};
    applyFacetFilters(where, { safetyBand: ['review', 'unknown'] });
    expect(where).toEqual({
      _or: [{ safety_band: { _in: ['review', 'unknown'] } }, { safety_band: { _is_null: true } }],
    });
  });

  it('maps savings bounds onto estimated_savings', () => {
    const gte: any = {};
    applyFacetFilters(gte, { savingsGte: 5 });
    expect(gte).toEqual({ estimated_savings: { _gte: 5 } });

    const lt: any = {};
    applyFacetFilters(lt, { savingsLt: 0 });
    expect(lt).toEqual({ estimated_savings: { _lt: 0 } });
  });

  it('maps last-seen bounds onto updated_at', () => {
    const where: any = {};
    applyFacetFilters(where, { updatedAtGte: '2026-08-02T12:00:00.000Z' });
    expect(where).toEqual({ updated_at: { _gte: '2026-08-02T12:00:00.000Z' } });

    const stale: any = {};
    applyFacetFilters(stale, { updatedAtLt: '2026-07-04T12:00:00.000Z' });
    expect(stale).toEqual({ updated_at: { _lt: '2026-07-04T12:00:00.000Z' } });
  });

  it('leaves existing where fields untouched', () => {
    const where: any = { severity: { _in: ['Critical'] } };
    applyFacetFilters(where, { safetyBand: ['safe'], savingsGte: 1 });
    expect(where.severity).toEqual({ _in: ['Critical'] });
    expect(where.safety_band).toEqual({ _in: ['safe'] });
    expect(where.estimated_savings).toEqual({ _gte: 1 });
  });
});
