import { resourceCreatedAt } from '../Instances';

// The storage Instances list is shared by AWS S3, GCP Cloud Storage and Azure Storage
// Accounts. resourceCreatedAt is what feeds the "Created At" column — it must prefer the
// cloud's own creation time (resourse_created_on) and never show the Go zero time a
// collector writes when a provider reports none. Mirrors the backend resourceCreatedAt
// (PR #39460). Regression guard for #39726.
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

  it('falls back to created_at when the provider reports no creation time (Go zero time)', () => {
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
