-- sha256(token) for Personal Access Tokens (provider_type='credentials', provider='token').
-- Lets the gateway validate a raw bearer token as a lone static key by looking the row up
-- by the token value alone. bcrypt (the existing `credential`) is salted and cannot be
-- looked up by value; sha256 of a high-entropy 32-byte random token is the right primitive.
-- Nullable: existing tokens stay NULL until lazily backfilled on their next login.
ALTER TABLE public.user_auths ADD COLUMN IF NOT EXISTS token_sha256 text;

-- Partial unique index: serves the gateway's `WHERE token_sha256 = $1` lookup and guarantees
-- a single-row resolve. Existing NULL rows are excluded, so this is a no-op for them.
CREATE UNIQUE INDEX IF NOT EXISTS user_auths_token_sha256_key
  ON public.user_auths (token_sha256)
  WHERE token_sha256 IS NOT NULL;
