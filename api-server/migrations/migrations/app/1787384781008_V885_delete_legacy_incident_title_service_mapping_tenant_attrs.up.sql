-- Legacy title->service JSON blobs in tenant_attrs (DATADOG/PAGERDUTY/ZENDUTY
-- _INCIDENT_TITLE_SERVICE_MAPPING) were copied into the dedicated
-- webhook_subject_mappings table by V808. That backfill is done, so the
-- tenant_attrs rows are no longer needed as the source of truth.
DELETE FROM tenant_attrs
WHERE name IN (
    'DATADOG_INCIDENT_TITLE_SERVICE_MAPPING',
    'PAGERDUTY_INCIDENT_TITLE_SERVICE_MAPPING',
    'ZENDUTY_INCIDENT_TITLE_SERVICE_MAPPING'
);
