-- No safe inverse. This migration reconciled rows whose user dismissal had been
-- silently reverted; the pre-migration state (which Dismissed rows were orphaned
-- Open/Archive rows vs. genuine dismissals) is not recoverable, and rolling back
-- must not un-dismiss a finding a user chose to dismiss. Intentionally a no-op.
SELECT 1;
