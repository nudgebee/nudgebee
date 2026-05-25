INSERT INTO "public"."feature"("description", "value")
VALUES ('Skip service label check for event investigation - allows investigation even when service/services labels are missing', 'EVENT_INVESTIGATION_SKIP_SERVICE_LABEL_CHECK')
ON CONFLICT (value) DO NOTHING;
