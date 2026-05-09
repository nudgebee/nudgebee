-- Create rag_embedding_token_usage table for tracking embedding API token usage
CREATE TABLE public.rag_embedding_token_usage (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    account_id text NOT NULL,                -- UUID string or "global" for global embeddings
    is_global boolean DEFAULT false NOT NULL, -- TRUE for embeddings applicable to all accounts
    collection_name text NOT NULL,
    embedding_provider text NULL,
    embedding_model text NULL,
    total_tokens int8 DEFAULT 0 NOT NULL,
    document_count int4 DEFAULT 0 NOT NULL,
    operation_type text DEFAULT 'batch_embedding' NOT NULL,
    batch_id uuid NULL,
    request_status text DEFAULT 'success' NOT NULL,
    error_message text NULL,
    created_at timestamp DEFAULT now() NOT NULL,
    updated_at timestamp DEFAULT now() NOT NULL,

    CONSTRAINT rag_embedding_token_usage_pk PRIMARY KEY (id),
    CONSTRAINT rag_embedding_token_usage_status_check
        CHECK ((request_status = ANY (ARRAY['success'::text, 'partial'::text, 'failure'::text])))
);

COMMENT ON TABLE public.rag_embedding_token_usage IS
'Token usage tracking for RAG embedding API calls. Tracks tokens consumed during document embedding operations. account_id is "global" and is_global=TRUE for embeddings applicable to all accounts.';