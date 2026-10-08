-- AI Gateway per-user access switch (#38963): a tenant admin suspends one user's gateway access,
-- or ends it on a date, across every key the user owns. No row = active (every user today).
-- Matched on (tenant_id, user_id): a user's rules belong to the tenant that set them, while a
-- key's own rows follow the key (see V985). A new, empty table: no lock on any existing table.
CREATE TABLE IF NOT EXISTS public.llm_gateway_user_access (
    tenant_id   text        NOT NULL,
    user_id     text        NOT NULL,
    suspended   boolean     NOT NULL DEFAULT false,
    valid_until timestamptz,
    PRIMARY KEY (tenant_id, user_id)
);
