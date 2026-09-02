-- Retire eBPF ExternalService nodes whose AWS hostname was truncated at the
-- region (the sensor drops the trailing "-<N>.amazonaws.com"), e.g.
-- "<bucket>.s3.us-east" or "<account>.dkr.ecr.us-east". Before the classifier
-- fix (issue #35673) these failed AWS hostname classification and persisted as
-- duplicate external nodes sitting beside the real S3 Storage / ECR
-- ContainerRegistry node.
--
-- With the fix, on the next graph build these hostnames classify and collapse
-- onto the real cloud-resource node, so the stale ExternalService rows stop
-- being re-stamped. ExternalService is NOT tombstone-eligible
-- (markInactiveNodes protects non-infra flow-sourced nodes), so those rows
-- would otherwise linger is_active=true forever. Soft-delete them here; the
-- next per-tenant rebuild re-derives the correct graph. Idempotent.
--
-- Scope is limited to the genuinely-collapsible per-resource forms (S3, ECR).
-- Bare service-API endpoints (kms.us-east, ec2.us-east, …) are left untouched:
-- the eBPF flow source re-creates them every build, so they are correct as
-- ExternalService stubs and deactivating them would only cause a flicker.
UPDATE knowledge_graph_node
SET is_active  = false,
    updated_at = NOW()
WHERE node_type = 'ExternalService'
  AND is_active = true
  -- Case-insensitive (~*) to mirror reconstructTruncatedAWSHost, which lowercases
  -- the hostname before matching, so this retires exactly the set the classifier
  -- now collapses.
  AND properties->>'name' ~* '\.(us|eu|ap|sa|ca|af|me|cn|il|mx)-[a-z]+(-[a-z]+)?$'
  AND (
        properties->>'name' ~* '\.s3\.'
     OR properties->>'name' ~* '\.ecr\.'
      );
