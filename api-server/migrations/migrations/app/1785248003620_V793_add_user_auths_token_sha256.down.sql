DROP INDEX IF EXISTS user_auths_token_sha256_key;
ALTER TABLE public.user_auths DROP COLUMN IF EXISTS token_sha256;
