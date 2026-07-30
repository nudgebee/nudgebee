DROP INDEX IF EXISTS idx_gateway_request_log_session_created;
ALTER TABLE llm_gateway_request_log DROP COLUMN IF EXISTS first_user_message;
