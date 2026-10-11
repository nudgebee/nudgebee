-- Last 4 characters of a key, written at create so admins can tell keys apart (#38965).
-- Older keys keep '' (only hashes are stored). Metadata-only ADD COLUMN with a constant default: no rewrite.
ALTER TABLE public.user_auths ADD COLUMN IF NOT EXISTS token_suffix text NOT NULL DEFAULT '';
