-- Tombstone phantom inferred nodes that the flow-source enricher synthesized
-- from bare AWS service-API hostnames (e.g. ec2.us-east-2.amazonaws.com,
-- sqs.us-east-1.amazonaws.com, api.sagemaker.us-east-1.amazonaws.com,
-- public.ecr.aws). These hosts are shared by every AWS customer in a
-- region/account and do not name a graph entity — see
-- knowledge_graph/flow_sources/enrichment_aws_classifier.go:IsBareAWSServiceEndpoint
-- for the structural rule and the companion code-side fix.
--
-- The predicate here mirrors the Go rule exactly. The region regex is what
-- distinguishes `<service>.<region>` (bare, tombstoned) from
-- `<bucket>.<service>` (per-resource, kept) — both have 2 dot-separated
-- parts before .amazonaws.com but only the former's right label looks like
-- an AWS region.
--
-- We only touch rows tagged by the inferred-node code path (the
-- discovery_method/inferred properties were stamped exclusively by
-- createInferredNodeIfAWS), so AWS-source-originated CloudResource /
-- MessageQueue / SecretVault rows for real customer resources are untouched
-- even if their dns_name property happens to match the bare pattern.

WITH candidates AS (
    SELECT
        id,
        lower(properties->>'name') AS host
    FROM knowledge_graph_node
    WHERE is_active = true
      AND (properties->>'inferred')::boolean = true
      AND properties->>'discovery_method' = 'aws_hostname_pattern'
),
parsed AS (
    SELECT
        id,
        host,
        regexp_replace(host, '\.amazonaws\.com$', '') AS rest,
        string_to_array(regexp_replace(host, '\.amazonaws\.com$', ''), '.') AS parts
    FROM candidates
    WHERE host = 'public.ecr.aws'
       OR host LIKE '%.amazonaws.com'
)
UPDATE knowledge_graph_node n
SET is_active = false,
    updated_at = now()
FROM parsed
WHERE n.id = parsed.id
  AND (
      parsed.host = 'public.ecr.aws'
      OR array_length(parsed.parts, 1) = 1
      OR (
          array_length(parsed.parts, 1) = 2
          AND parsed.parts[2] ~ '^[a-z]{2}-[-a-z]+-[0-9]+$'
      )
      OR (
          array_length(parsed.parts, 1) = 3
          AND parsed.parts[1] = 'api'
          AND parsed.parts[3] ~ '^[a-z]{2}-[-a-z]+-[0-9]+$'
      )
  );
