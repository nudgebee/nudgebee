-- Records when a message turn's foreground answer finished (any terminal status),
-- so the UI can show answer duration without including post-response background work
-- (memory extraction, summary, suggestions) that bumps updated_at after the answer.
alter table llm_conversation_messages
add column if not exists responded_at timestamp without time zone;
