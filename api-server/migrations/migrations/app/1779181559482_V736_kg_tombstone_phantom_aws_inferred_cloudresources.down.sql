-- Best-effort inverse of the up migration: reactivates inferred phantom nodes
-- that match the same bare-AWS-service-endpoint predicate. Not a strict inverse
-- — a row deactivated for an unrelated reason between up and down would be
-- reactivated here too. In practice the predicate is narrow enough
-- (inferred=true AND discovery_method='aws_hostname_pattern' AND bare-host
-- shape) that the false-positive surface is empty.

WITH candidates AS (
    SELECT
        id,
        lower(properties->>'name') AS host
    FROM knowledge_graph_node
    WHERE is_active = false
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
SET is_active = true,
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
