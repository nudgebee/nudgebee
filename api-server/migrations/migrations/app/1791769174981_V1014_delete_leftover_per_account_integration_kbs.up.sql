-- Delete per-account integration knowledge bases left over from the V1013 fold
-- (#40605).
--
-- V1013 folded every (integration, account) row into one organisation row per
-- integration and deleted the per-account rows. While that release rolled out,
-- llm-server pods still on the previous release ran their knowledge-base sync
-- and created a fresh per-account row for each linked account. Current code
-- never reads or writes those rows, so they only sit in the table (on dev: 40
-- rows in status 'error'). The organisation rows keep the integration's
-- documents; this deletes no collection.
--
-- Nothing points at these rows: old pods could not save agent mappings in that
-- window (see V1013), and the tables that reference llm_knowledgebases delete
-- on cascade. Idempotent: a second run finds no rows.
--
-- Lock window: row locks on the deleted rows and their cascaded children, well
-- under a second.

DELETE FROM public.llm_knowledgebases
WHERE account_id IS NOT NULL
  AND integration_id IS NOT NULL;
