-- No down migration.
--
-- Reactivating these rows would restore the defect's output: ~10k finished CI
-- runner pods presented as live services. The rows are tombstoned rather than
-- deleted, so nothing is lost until nb.CleanupData reclaims them on its normal
-- retention schedule; a genuine rollback of the code fix simply leaves them
-- inactive, and any node the agent still reports is re-activated by the next
-- Knowledge Graph build's upsert.
SELECT 1;
