CREATE TABLE IF NOT EXISTS llm_conversation_attachments (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id      UUID        NOT NULL,
    content_hash    TEXT        NOT NULL,
    mime_type       TEXT        NOT NULL,
    size_bytes      INTEGER     NOT NULL DEFAULT 0,
    data            TEXT,
    source_url      TEXT,
    description     TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Dedup: one copy per account + content hash
CREATE UNIQUE INDEX idx_llm_conv_attachments_account_content_hash
    ON llm_conversation_attachments (account_id, content_hash);

-- Retention cleanup: find rows by last_used_at so reused images survive
CREATE INDEX idx_llm_conv_attachments_retention
    ON llm_conversation_attachments (account_id, last_used_at)
    WHERE data IS NOT NULL;

CREATE TABLE IF NOT EXISTS llm_conversation_attachment_refs (
    attachment_id    UUID        NOT NULL REFERENCES llm_conversation_attachments(id) ON DELETE CASCADE,
    message_id       UUID        NOT NULL,
    conversation_id  UUID        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (attachment_id, message_id)
);

-- Lookup refs by message
CREATE INDEX idx_llm_conv_attachment_refs_message_id
    ON llm_conversation_attachment_refs (message_id);

-- Lookup refs by conversation
CREATE INDEX idx_llm_conv_attachment_refs_conversation_id
    ON llm_conversation_attachment_refs (conversation_id);
