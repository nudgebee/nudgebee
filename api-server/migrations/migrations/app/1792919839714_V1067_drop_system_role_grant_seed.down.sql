-- No-op. The dropped rows were an unread copy of a grant set derived from
-- actions.yaml (app/src/lib/systemRoleGrants.ts deriveSystemRoleGrants); if a
-- future resolver needs them, it should derive them from that function rather
-- than restore a snapshot.
SELECT 1;
