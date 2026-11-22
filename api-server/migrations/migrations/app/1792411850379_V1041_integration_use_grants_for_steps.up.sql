-- Automation steps that act through an integration now need that integration's
-- use grant, integrations.<type> (Read for a read step, Execute for a change),
-- instead of a grant on the area they used to be filed under: ticket steps
-- were tickets:*, database steps database:*, ArgoCD k8s:*, and the SSH,
-- RabbitMQ, source-control CLI and MCP steps integrations:Write. A script over
-- SSH now also needs integrations.ssh:Execute next to workflows:Write.
--
-- This gives each tenant custom role the use grants that keep the steps it
-- could run before. The old grants stay: they still gate the app's own actions
-- (tickets_create, database_performance_insights, ...).
--
--   tickets:Write            -> Execute on jira, servicenow, pagerduty, zenduty, freshdesk
--   tickets:Read | Write     -> Read on those and github, gitlab
--   database:Write           -> Execute on mysql, postgresql, clickhouse, mssql, oracle, redis
--   k8s:Write                -> Execute on argocd
--   integrations:Write       -> Execute on ssh, rabbitmq, github, gitlab, mcp
--   workflows:Write          -> Execute on ssh
--
-- Only Write converts to Execute: those steps needed Write before, and an
-- Execute grant on tickets, database or k8s ran none of them.
--
-- Deliberately NOT converted, so these roles lose a step until an admin grants
-- the integration's use (the step is refused with a message naming the grant):
--   * tickets:* and k8s:* to github / gitlab Execute: integrations.github:Execute
--     also runs the GitHub CLI step (any gh command with the integration's
--     token), which neither grant allowed. Affects GitHub/GitLab tickets and
--     GitOps pull requests. integrations:Write already ran that CLI, so it
--     does convert.
--   * k8s:Write to the ticket types: a rightsize step whose ticket setting
--     files a ticket instead of patching now needs that ticket integration's
--     Execute, and granting it from k8s:Write would also open every ticket
--     step on it.
--
-- Skipped: system roles (built-in roles are enforced by role, not by these
-- rows) and roles held by a bee account (a bee already needed the integration
-- grant on top, and its install computes its grants per step).
--
-- One INSERT, row locks only, idempotent: ON CONFLICT on the
-- (custom_role_id, module, class) key makes a rerun a no-op. Account scope
-- lives on the role's assignments, so the new rows apply where the old did.
INSERT INTO "public"."custom_role_permissions" ("custom_role_id", "module", "class")
SELECT DISTINCT crp."custom_role_id", 'integrations.' || m."type", m."to_class"
FROM "public"."custom_role_permissions" crp
JOIN "public"."custom_roles" cr
  ON cr."id" = crp."custom_role_id" AND NOT coalesce(cr."is_system", false)
JOIN (VALUES
    ('tickets', 'Write',   'jira',       'Execute'),
    ('tickets', 'Write',   'servicenow', 'Execute'),
    ('tickets', 'Write',   'pagerduty',  'Execute'),
    ('tickets', 'Write',   'zenduty',    'Execute'),
    ('tickets', 'Write',   'freshdesk',  'Execute'),
    ('tickets', 'Read',    'jira',       'Read'),
    ('tickets', 'Read',    'github',     'Read'),
    ('tickets', 'Read',    'gitlab',     'Read'),
    ('tickets', 'Read',    'servicenow', 'Read'),
    ('tickets', 'Read',    'pagerduty',  'Read'),
    ('tickets', 'Read',    'zenduty',    'Read'),
    ('tickets', 'Read',    'freshdesk',  'Read'),
    ('tickets', 'Write',   'github',     'Read'),
    ('tickets', 'Write',   'gitlab',     'Read'),
    ('database', 'Write',   'mysql',      'Execute'),
    ('database', 'Write',   'postgresql', 'Execute'),
    ('database', 'Write',   'clickhouse', 'Execute'),
    ('database', 'Write',   'mssql',      'Execute'),
    ('database', 'Write',   'oracle',     'Execute'),
    ('database', 'Write',   'redis',      'Execute'),
    ('k8s', 'Write',   'argocd', 'Execute'),
    ('integrations', 'Write', 'ssh',      'Execute'),
    ('integrations', 'Write', 'rabbitmq', 'Execute'),
    ('integrations', 'Write', 'github',   'Execute'),
    ('integrations', 'Write', 'gitlab',   'Execute'),
    ('integrations', 'Write', 'mcp',      'Execute'),
    ('workflows', 'Write', 'ssh', 'Execute')
) AS m ("from_module", "from_class", "type", "to_class")
  ON crp."module" = m."from_module" AND crp."class" = m."from_class"
WHERE NOT EXISTS (
    SELECT 1
    FROM "public"."custom_role_assignments" cra
    JOIN "public"."users" u ON u."id" = cra."principal_id"
    WHERE cra."custom_role_id" = crp."custom_role_id"
      AND cra."principal_type" = 'user'
      AND u."kind" = 'bee'
)
ON CONFLICT ("custom_role_id", "module", "class") DO NOTHING;
