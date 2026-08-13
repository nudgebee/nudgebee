-- V814: repoint runbook (KB) → agent mappings from the legacy *_debug names onto
-- the canonical *_orchestrator names.
--
-- The five cloud orchestrators were renamed *_debug → *_orchestrator; the old names
-- were kept as back-compat invocation aliases, but the KB pre-step looked up mappings
-- by the agent's current name only, so every runbook a user mapped under the old name
-- became invisible to the renamed agent (no <skill-lists>, no load_skills). The code
-- fallback (skill lookup now also queries aliases) fixes this going forward; this
-- migration consolidates the stored data onto the canonical key so both agree.
--
-- llm_kb_agent_mappings has a UNIQUE (kb_id, agent_id). A KB already mapped to BOTH
-- the old and the new name would collide on rename, so we only repoint rows that have
-- no canonical counterpart, then delete the now-redundant *_debug rows.

UPDATE llm_kb_agent_mappings m
SET agent_id = CASE m.agent_id
        WHEN 'k8s_debug'     THEN 'k8s_orchestrator'
        WHEN 'aws_debug'     THEN 'aws_orchestrator'
        WHEN 'gcp_debug'     THEN 'gcp_orchestrator'
        WHEN 'azure_debug'   THEN 'azure_orchestrator'
        WHEN 'datadog_debug' THEN 'datadog_orchestrator'
    END,
    updated_at = NOW()
WHERE m.agent_id IN ('k8s_debug', 'aws_debug', 'gcp_debug', 'azure_debug', 'datadog_debug')
  AND NOT EXISTS (
      SELECT 1
      FROM llm_kb_agent_mappings x
      WHERE x.kb_id = m.kb_id
        AND x.agent_id = CASE m.agent_id
              WHEN 'k8s_debug'     THEN 'k8s_orchestrator'
              WHEN 'aws_debug'     THEN 'aws_orchestrator'
              WHEN 'gcp_debug'     THEN 'gcp_orchestrator'
              WHEN 'azure_debug'   THEN 'azure_orchestrator'
              WHEN 'datadog_debug' THEN 'datadog_orchestrator'
          END
  );

-- Remove any *_debug rows that survived the UPDATE because a canonical mapping for the
-- same KB already existed (the runbook is already reachable under the canonical name).
DELETE FROM llm_kb_agent_mappings
WHERE agent_id IN ('k8s_debug', 'aws_debug', 'gcp_debug', 'azure_debug', 'datadog_debug');
