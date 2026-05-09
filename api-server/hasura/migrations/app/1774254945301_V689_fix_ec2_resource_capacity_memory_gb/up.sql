-- Fix cloud_resource_details rows where memory_gb was stored as a raw AWS string
-- (e.g., "1 GiB", "16 GiB") instead of a numeric value.
-- This occurred when fetchAndStoreEc2Price stored the raw Pricing API response.
-- Converts "1 GiB" → 1, "1,952 GiB" → 1952, "512 MiB" → 0.5, etc.

UPDATE cloud_resource_details
SET resource_capacity = jsonb_set(
    resource_capacity,
    '{memory_gb}',
    to_jsonb(
        CASE
            WHEN resource_capacity->>'memory_gb' LIKE '% MiB' THEN
                CAST(
                    REPLACE(SPLIT_PART(resource_capacity->>'memory_gb', ' ', 1), ',', '')
                    AS DECIMAL
                ) / 1024
            WHEN resource_capacity->>'memory_gb' LIKE '% TiB' THEN
                CAST(
                    REPLACE(SPLIT_PART(resource_capacity->>'memory_gb', ' ', 1), ',', '')
                    AS DECIMAL
                ) * 1024
            ELSE
                CAST(
                    REPLACE(SPLIT_PART(resource_capacity->>'memory_gb', ' ', 1), ',', '')
                    AS DECIMAL
                )
        END
    )
)
WHERE cloud_provider = 'aws'
  AND service_name = 'AmazonEC2'
  AND jsonb_typeof(resource_capacity->'memory_gb') = 'string'
  AND (
      resource_capacity->>'memory_gb' LIKE '% GiB'
      OR resource_capacity->>'memory_gb' LIKE '% MiB'
      OR resource_capacity->>'memory_gb' LIKE '% TiB'
  );
