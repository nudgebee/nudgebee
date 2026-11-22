// Shared "created at" resolver for cloud resource lists (Cloud Storage buckets,
// ECS services, …). resourse_created_on is the cloud's own creation time,
// normalized by the collector (GCS `Created`, S3 CreationDate, Azure CreationTime,
// ECS service CreatedAt). Prefer it over the per-provider meta keys the UI used to
// guess at and get wrong. When a provider reports no creation time the collector
// stores Go's zero time ("0001-01-01T00:00:00Z"), so treat a pre-1970 year as
// missing and fall back to the meta fields, then created_at (when NudgeBee first
// saw the resource). Mirrors the backend's resourceCreatedAt (PR #39460).
export const resourceCreatedAt = (item: any): string | undefined => {
  const cloudCreatedOn = item?.resourse_created_on;
  if (cloudCreatedOn && new Date(cloudCreatedOn).getUTCFullYear() > 1970) {
    return cloudCreatedOn;
  }
  return item?.meta?.CreationDate || item?.meta?.timeCreated || item?.created_at;
};
