-- Migration: Strip "enc:" prefix from encrypted integration config values.
-- The prefix was added unnecessarily; encrypted values are already identified
-- by the is_encrypted column. This restores them to plain hex ciphertext.

UPDATE integration_config_values
SET value = substring(value FROM 5),
    updated_at = NOW()
WHERE is_encrypted = true
  AND value LIKE 'enc:%';
