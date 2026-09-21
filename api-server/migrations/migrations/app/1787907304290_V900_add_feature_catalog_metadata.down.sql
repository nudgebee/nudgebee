-- Restores the descriptions this migration overwrote, then removes the columns
-- and the lookup table. Descriptions are restored verbatim from what was live
-- before V900, so a rollback leaves the catalog exactly as it was found.
UPDATE public.feature AS f SET description = o.description
FROM (VALUES
  ('GENERATE_RCA',                                 'Get root cause analysis for events'),
  ('EVENT_AUTO_AI_SUMMARY',                        'Automatically Summerize summaries of Events data'),
  ('EVENT_DEBUG_ANALYSIS_DISABLED',                'Disable debug analysis for events at account level'),
  ('EVENT_INVESTIGATION_SKIP_SERVICE_LABEL_CHECK', 'Skip service label check for event investigation - allows investigation even when service/services labels are missing'),
  ('EVENT_AUTO_RAISE_PR_ENABLED',                  'Automatically raise a code-fix PR when an event log analysis localizes a code-level root cause'),
  ('EVENT_ANALYSIS_ON_CHANNEL',                    'Enable event-analysis notifications to incident channels'),
  ('TRIAGE_LLM_SCORING',                           'LLM-assisted triage scoring: classify each alert into a per-class verdict and derive a real P0–P3 priority with a deterministic policy, instead of the legacy severity×environment formula. Per-tenant enrolment during rollout.'),
  ('TROUBLESHOOT',                                 'AI-powered incident troubleshooting and investigation'),
  ('MEMORY_MODULE',                                'Layered memory architecture for LLM agents (soul, preferences, patterns, decisions, policy, account context, session working memory, heartbeat, collective). Per-tenant enrolment during rollout.'),
  ('AI_WORKFLOW_TOOLS',                            'Lets the AI assistant discover and invoke automations that have been explicitly opted in (workflows.ai_invocable), and run curated built-in automation actions. Every AI-initiated run still requires in-chat user confirmation and the user''s own permissions.'),
  ('CHANNEL_AWARENESS',                            'Nubi passively follows opted-in messaging channels and uses the conversation as context when mentioned'),
  ('LLM_FUNCTION',                                 'llm function related changes like creating/editing function and at alert'),
  ('LLM_ANALYSER',                                 'LLM Analyser cost/usage tab on the Optimise page'),
  ('AI_COST_REPORT',                               'AI/LLM cost report: daily + month-to-date Slack digest and dashboard Accounts tab'),
  ('OPENCOST_SERVER_SIDE_SPEND',                   'Default-ON kill switch for the server-side OpenCost spend-sync cron. Enrolment is automatic for connected K8s clusters whose agent has its own OpenCost disabled; set this flag to ''disabled'' to force a tenant''s clusters off the server-side path.'),
  ('UPGRADE_PLANNER',                              'Analyzes your current Kubernetes cluster configuration and generates a step-by-step upgrade plan '),
  ('OPTIMIZE',                                     'AI-powered cost optimisation and recommendations'),
  ('ANOMALY_DETECTION',                            'Metric and spend anomaly detection (CPU, memory, latency, replicas, error rate, cloud spend). Opt-in: a tenant sees anomalies only with an ''enabled'' feature_flag row.'),
  ('ANOMALY_DETECTION_ERROR_RATE',                 'Error-rate metric anomaly detection. Default ON; set this flag to ''disabled'' for a tenant to suppress error-rate anomalies while leaving the other detectors on.'),
  ('WEBHOOK_LLM_RESOLUTION',                       'Enable LLM-based subject resolution for webhook alerts'),
  ('RBAC_K8S',                                     'K8S Rbac Support'),
  ('CUSTOM_ROLES',                                 'Tenant-defined custom roles carrying (module x class) permission grants, additive to the built-in roles. Per-tenant enrolment during rollout.')
) AS o(value, description)
WHERE f.value = o.value;

ALTER TABLE public.feature
    DROP CONSTRAINT IF EXISTS feature_category_fkey,
    DROP CONSTRAINT IF EXISTS feature_polarity_check,
    DROP COLUMN IF EXISTS display_name,
    DROP COLUMN IF EXISTS category,
    DROP COLUMN IF EXISTS polarity,
    DROP COLUMN IF EXISTS stored_value_inverted;

DROP TABLE IF EXISTS public.feature_category;

-- Restores the catalog entry only. The per-tenant rows are NOT restored: they
-- recorded 'enabled' on a default-ON flag, so their absence leaves every tenant
-- in the same state the rows described.
INSERT INTO public.feature (value, description)
VALUES ('VERTICAL_RIGHTSIZING', 'Enable vertical rightsizing recommendations for K8s workloads')
ON CONFLICT (value) DO NOTHING;

-- Recreates the three retired catalog entries. Per-tenant rows are NOT
-- restored: every one of them was either 'disabled' on an opt-in flag that was
-- on nowhere, or absent entirely, so leaving them out reproduces the state the
-- rows described. The UPDATE above is a no-op for these three, since the rows
-- it targets no longer exist by the time it runs.
INSERT INTO public.feature (value, description) VALUES
  ('RBAC_K8S', 'K8S Rbac Support'),
  ('ANOMALY_DETECTION_ERROR_RATE', 'Error-rate metric anomaly detection. Default ON; set this flag to ''disabled'' for a tenant to suppress error-rate anomalies while leaving the other detectors on.'),
  ('OPENCOST_SERVER_SIDE_SPEND', 'Default-ON kill switch for the server-side OpenCost spend-sync cron. Enrolment is automatic for connected K8s clusters whose agent has its own OpenCost disabled; set this flag to ''disabled'' to force a tenant''s clusters off the server-side path.')
ON CONFLICT (value) DO NOTHING;
