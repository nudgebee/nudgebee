-- Registers the alert remediation loop (#40734, docs/incident-agent-v1.md); registering does NOT enable it.
-- Tenant-wide: only the tenant row (account_id IS NULL) enables it, via Settings > Tenant Settings > Feature Flags.
INSERT INTO public.feature (value, display_name, category, polarity, description)
VALUES (
    'EVENT_REMEDIATION_LOOP',
    'Alert remediation loop',
    'events',
    'opt_in',
    'Builds a fix plan for each alert after its investigation and posts it in the alert''s Slack thread, where each step can be run or declined; after a step runs, checks the result read-only and proposes what comes next. Every step still needs a person to run it. Costs AI calls per alert. Off means alerts are investigated as before, with no plan posted and no follow-up.'
)
ON CONFLICT (value) DO UPDATE SET
    display_name = EXCLUDED.display_name,
    category     = EXCLUDED.category,
    polarity     = EXCLUDED.polarity,
    description  = EXCLUDED.description;
