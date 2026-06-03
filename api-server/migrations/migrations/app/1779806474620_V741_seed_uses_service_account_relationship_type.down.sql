-- Reverts the USES_SERVICE_ACCOUNT seed.
-- Safe only if no edges still reference it — the FK from knowledge_graph_edge
-- back here would block this DELETE if any USES_SERVICE_ACCOUNT edges exist.
-- In practice you'd run this paired with a tombstone of any rows that
-- reference it; otherwise leave it in place.
DELETE FROM knowledge_graph_relationship_types
WHERE name = 'USES_SERVICE_ACCOUNT';
