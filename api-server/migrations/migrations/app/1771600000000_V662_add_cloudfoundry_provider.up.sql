-- Add CloudFoundry as a cloud provider enum value
INSERT INTO "public"."cloud_provider_type"("value", "comment")
VALUES ('CloudFoundry', 'Cloud Foundry')
ON CONFLICT ("value") DO NOTHING;
