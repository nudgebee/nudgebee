-- Purge the stored credentials from failed cloud-account onboardings.
--
-- cloud_account_onboarding_errors.config held the whole create request:
-- AWS access secret, Azure client secret, GCP service-account key,
-- CloudFoundry client secret. It was encrypted as one blob under
-- NUDGEBEE_ENCRYPTION_KEY, so SQL cannot redact inside it. Nothing reads the
-- column, so this clears it. Each row keeps tenant, user, provider, time and
-- error. New rows store a redacted, encrypted copy (onboardingErrorConfig in
-- account/service.go).
--
-- Lock window: one UPDATE over a small table, holding row locks only. The
-- column has no readers, and the only writer is a plain INSERT, which those
-- locks do not block. Rows that the new code writes before this migration runs
-- are cleared too, which loses only a few minutes of diagnostic copies.
UPDATE cloud_account_onboarding_errors
SET config = ''
WHERE config <> '';
