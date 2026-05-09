-- Rollback: Re-add "enc:" prefix to encrypted integration config values.

UPDATE integration_config_values
SET value = 'enc:' || value,
    updated_at = NOW()
WHERE is_encrypted = true
  AND value NOT LIKE 'enc:%';
