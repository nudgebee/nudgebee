-- Step 1: Delete dependent rows from feature_flag
DELETE FROM
    feature_flag
WHERE
    feature_id IN (
        'GENERATE_RCA',
        'NB_SLM',
        'CHAT_SUGGESTIONS',
        'LLM_BASED_CHAT',
        'ACCOUNT_GRAFANA',
        'WEEKLY_SPEND_EMAIL_NOTIFICATION',
        'NOTIFICATION_DAILY_HIGHLIGHT_REPORT',
        'BILLING_ENABLED',
        'LLM_CHAT_FOLLOWUP',
        'RUNBOOK_PROM_QL_TRIGGER'
    );

-- Step 2: Delete from the feature table
DELETE FROM
    feature
WHERE
    value IN (
        'GENERATE_RCA',
        'NB_SLM',
        'CHAT_SUGGESTIONS',
        'LLM_BASED_CHAT',
        'ACCOUNT_GRAFANA',
        'WEEKLY_SPEND_EMAIL_NOTIFICATION',
        'NOTIFICATION_DAILY_HIGHLIGHT_REPORT',
        'BILLING_ENABLED',
        'LLM_CHAT_FOLLOWUP',
        'RUNBOOK_PROM_QL_TRIGGER'
    );