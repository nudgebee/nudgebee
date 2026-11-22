import { resourceCreatedAt } from '../resourceCreatedAt';

// resourceCreatedAt feeds the "Created At" column of every cloud resource list
// (Cloud Storage buckets, ECS services, …). It must prefer the cloud's own creation
// time (resourse_created_on) and never show the Go zero time a collector writes when
// a provider reports none. Mirrors the backend resourceCreatedAt (PR #39460).
// Regression guard for #39726 (GCS buckets) and #39708 (ECS).
describe('resourceCreatedAt', () => {
  it('uses resourse_created_on for a GCS bucket whose meta only carries `Created` (#39726)', () => {
    const item = {
      resourse_created_on: '2025-10-13T08:30:00Z',
      meta: { Created: '2025-10-13T08:30:00Z' },
      created_at: '2026-09-28T16:23:00Z',
    };
    expect(resourceCreatedAt(item)).toBe('2025-10-13T08:30:00Z');
  });

  it('uses resourse_created_on for an AWS S3 bucket', () => {
    const item = {
      resourse_created_on: '2024-01-02T00:00:00Z',
      meta: { CreationDate: '2024-01-02T00:00:00Z' },
      created_at: '2026-09-28T16:23:00Z',
    };
    expect(resourceCreatedAt(item)).toBe('2024-01-02T00:00:00Z');
  });

  it('uses resourse_created_on for an ECS service, not the first-seen time (#39708)', () => {
    const item = {
      resourse_created_on: '2025-03-20T11:00:00Z',
      meta: { CreatedAt: '2025-03-20T11:00:00Z' },
      created_at: '2026-09-28T16:23:00Z',
    };
    expect(resourceCreatedAt(item)).toBe('2025-03-20T11:00:00Z');
  });

  it('falls back to created_at when the provider reports no creation time (Go zero time)', () => {
    // e.g. an ECS cluster: AWS DescribeClusters returns no creation time, so the
    // collector stores the zero value. The cluster list relabels its column to
    // "First Seen" rather than relying on this fallback, but the guard still holds.
    const item = {
      resourse_created_on: '0001-01-01T00:00:00Z',
      meta: {},
      created_at: '2026-09-28T16:23:00Z',
    };
    expect(resourceCreatedAt(item)).toBe('2026-09-28T16:23:00Z');
  });

  it('treats an epoch creation time as missing and falls back', () => {
    const item = {
      resourse_created_on: '1970-01-01T00:00:00Z',
      meta: {},
      created_at: '2026-09-28T16:23:00Z',
    };
    expect(resourceCreatedAt(item)).toBe('2026-09-28T16:23:00Z');
  });

  it('falls back through the legacy meta fields when resourse_created_on is absent', () => {
    expect(resourceCreatedAt({ meta: { CreationDate: '2023-05-01T00:00:00Z' }, created_at: '2026-09-28T16:23:00Z' })).toBe('2023-05-01T00:00:00Z');
    expect(resourceCreatedAt({ meta: { timeCreated: '2023-06-01T00:00:00Z' }, created_at: '2026-09-28T16:23:00Z' })).toBe('2023-06-01T00:00:00Z');
    expect(resourceCreatedAt({ meta: {}, created_at: '2026-09-28T16:23:00Z' })).toBe('2026-09-28T16:23:00Z');
  });
});
