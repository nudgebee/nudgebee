-- Restore the catalog entry so a license carrying `branding.custom` can be
-- bridged into feature_flag again. The flag rows themselves are not restored:
-- the license bridge re-creates them on its next reconcile tick for whichever
-- tenant's license still lists the feature.
INSERT INTO feature (value, description)
VALUES ('branding.custom', 'White-label branding: operator-supplied logo, colors, fonts and assets override the Nudgebee defaults')
ON CONFLICT (value) DO NOTHING;
