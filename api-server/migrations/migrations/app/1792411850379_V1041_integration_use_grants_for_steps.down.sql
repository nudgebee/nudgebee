-- Intentionally a no-op. The up migration only adds integrations.<type> use
-- grants to tenant custom roles; those rows cannot be told apart from the same
-- grants an admin ticks afterwards, so removing them could take away
-- deliberate grants. They are inert for code that predates them.
SELECT 1;
