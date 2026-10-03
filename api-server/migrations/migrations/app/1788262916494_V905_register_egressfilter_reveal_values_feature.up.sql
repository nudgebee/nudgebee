-- Register EGRESSFILTER_REVEAL_VALUES so it can be enabled per tenant via
-- feature_flag (whose feature_id FKs to feature.value). Gates whether the
-- egress-filter audit detail carries the raw matched value alongside the
-- character-class shape, for both secrets hits and PII values.
--
-- Default OFF: no feature_flag row means disabled, and the reader fails
-- closed on any lookup error. Enabling it causes raw secrets and PII to be
-- persisted verbatim into llm_conversation_messages.metadata and rendered in
-- the browser, so it is intended for testing environments only.
-- display_name/category are NOT NULL as of V900 (add_feature_catalog_metadata),
-- with no default for either; derived the same way V900's own backfill derives
-- one for any row that doesn't get an explicit display_name (initcap on the
-- value with underscores replaced), and category 'other' matching V900's
-- fallback for anything not in its named buckets.
INSERT INTO "public"."feature"("description", "value", "display_name", "category")
VALUES ('Egress filter: show the raw detected secret and PII values in audit details instead of only their masked shape (testing environments only — raw values are stored and displayed unmasked)', 'EGRESSFILTER_REVEAL_VALUES', 'Egressfilter Reveal Values', 'other')
ON CONFLICT (value) DO NOTHING;
