-- Inverse of the up migration: return Configuration+pod_right_sizing rows to
-- RightSizing. After the flip the producers keep writing both categories and
-- the archive sweep leaves archived twins under the stale category, so a
-- Configuration row can coexist with a RightSizing twin on the unique key
-- (status is not part of it). Drop the Configuration side of such pairs first
-- (the RightSizing twin is the row the pre-flip producers will keep updating),
-- then flip the rest.
DELETE FROM recommendation loser
WHERE loser.rule_name = 'pod_right_sizing'
  AND loser.category = 'Configuration'
  AND EXISTS (
    SELECT 1 FROM recommendation twin
    WHERE twin.cloud_account_id = loser.cloud_account_id
      AND twin.rule_name = loser.rule_name
      AND twin.category = 'RightSizing'
      AND COALESCE(twin.resource_id::text, '') = COALESCE(loser.resource_id::text, '')
      AND COALESCE(twin.account_object_id, '') = COALESCE(loser.account_object_id, '')
  );

UPDATE recommendation
SET category = 'RightSizing', updated_at = NOW()
WHERE rule_name = 'pod_right_sizing'
  AND category = 'Configuration';
