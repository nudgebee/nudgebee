-- Move pod_right_sizing rows where NO container has any cpu/memory request set
-- from category='RightSizing' to category='Configuration'. SQL twin of
-- classify_pod_right_sizing_category() in ml-k8s-server and k8s-collector:
-- allocated.request counts as SET only when it is a JSON number > 0
-- (null / missing / "?" / 0 all mean "not set").
--
-- In-place UPDATE (not archive+reinsert) on purpose: it preserves id,
-- created_at, is_dismissed, notes and resolution links, so the producers'
-- ON CONFLICT (cloud_account_id, rule_name, resource_id, category,
-- account_object_id) lands on this row instead of minting a new one — which
-- would re-fire runbook-server's created_at-based workflow poller and
-- resurrect dismissed recommendations.
--
-- No status filter: dismissed / closed / archived rows must follow their
-- category too, otherwise the next scan inserts a fresh Configuration row
-- alongside a dismissed RightSizing twin and the dismissal is silently lost.

-- Step 1: among the rows Step 2 will flip, drop any that already have a
-- Configuration+pod_right_sizing twin on the unique key (status is not part of
-- the key, so archiving would not dodge the collision). The twin is the
-- fresher producer-written row, so it wins. Nothing writes that combination
-- before this migration, so this is expected to delete 0 rows; it exists so
-- the migration is safe after a partial rollout. Scoped to flip candidates
-- only — a mixed-payload RightSizing row that stays put must never be deleted
-- in favor of a stale twin.
DELETE FROM recommendation loser
WHERE loser.rule_name = 'pod_right_sizing'
  AND loser.category = 'RightSizing'
  AND EXISTS (
    SELECT 1
    FROM jsonb_each(CASE WHEN jsonb_typeof(loser.recommendation) = 'object'
                         THEN loser.recommendation ELSE '{}'::jsonb END) AS c(container, entries)
    WHERE jsonb_typeof(c.entries) = 'array'
      -- compare against '[]' rather than jsonb_array_length(): Postgres does not
      -- guarantee the jsonb_typeof() guard is evaluated first, and the length
      -- function errors on a non-array. jsonb <> jsonb is safe for any type.
      AND c.entries <> '[]'::jsonb
  )
  AND NOT EXISTS (
    SELECT 1
    FROM jsonb_each(CASE WHEN jsonb_typeof(loser.recommendation) = 'object'
                         THEN loser.recommendation ELSE '{}'::jsonb END) AS c(container, entries)
    CROSS JOIN LATERAL jsonb_array_elements(
      CASE WHEN jsonb_typeof(c.entries) = 'array' THEN c.entries ELSE '[]'::jsonb END
    ) AS e(entry)
    WHERE jsonb_typeof(e.entry) = 'object'
      AND jsonb_typeof(e.entry #> '{allocated,request}') = 'number'
      AND (e.entry #>> '{allocated,request}')::numeric > 0
  )
  AND EXISTS (
    SELECT 1 FROM recommendation twin
    WHERE twin.cloud_account_id = loser.cloud_account_id
      AND twin.rule_name = loser.rule_name
      AND twin.category = 'Configuration'
      AND COALESCE(twin.resource_id::text, '') = COALESCE(loser.resource_id::text, '')
      AND COALESCE(twin.account_object_id, '') = COALESCE(loser.account_object_id, '')
  );

-- Step 2: recategorize the fully-unset rows.
UPDATE recommendation r
SET category = 'Configuration', updated_at = NOW()
WHERE r.rule_name = 'pod_right_sizing'
  AND r.category = 'RightSizing'
  -- at least one container entry: an empty or malformed payload must not
  -- vacuously classify as Configuration
  AND EXISTS (
    SELECT 1
    FROM jsonb_each(CASE WHEN jsonb_typeof(r.recommendation) = 'object'
                         THEN r.recommendation ELSE '{}'::jsonb END) AS c(container, entries)
    WHERE jsonb_typeof(c.entries) = 'array'
      -- compare against '[]' rather than jsonb_array_length(): Postgres does not
      -- guarantee the jsonb_typeof() guard is evaluated first, and the length
      -- function errors on a non-array. jsonb <> jsonb is safe for any type.
      AND c.entries <> '[]'::jsonb
  )
  -- ...and no entry carries a positive numeric allocated.request
  AND NOT EXISTS (
    SELECT 1
    FROM jsonb_each(CASE WHEN jsonb_typeof(r.recommendation) = 'object'
                         THEN r.recommendation ELSE '{}'::jsonb END) AS c(container, entries)
    CROSS JOIN LATERAL jsonb_array_elements(
      CASE WHEN jsonb_typeof(c.entries) = 'array' THEN c.entries ELSE '[]'::jsonb END
    ) AS e(entry)
    WHERE jsonb_typeof(e.entry) = 'object'
      AND jsonb_typeof(e.entry #> '{allocated,request}') = 'number'
      AND (e.entry #>> '{allocated,request}')::numeric > 0
  );
