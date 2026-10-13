-- Time to first streamed chunk on each AI Gateway streaming request, in ms. NULL for
-- non-streaming requests and for rows written before this. A nullable column with no
-- default is a metadata-only change (no table rewrite), so this takes only a brief lock.
ALTER TABLE llm_gateway_usage
    ADD COLUMN IF NOT EXISTS ttft_ms bigint;
