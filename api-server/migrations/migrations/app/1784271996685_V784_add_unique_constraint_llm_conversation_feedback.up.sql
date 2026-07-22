
-- Delete duplicate rows before adding the unique constraint, keeping the most recently updated one per group.
DELETE FROM public.llm_conversation_feedback
WHERE id IN (
    SELECT id
    FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY session_id, conversation_id, user_id, cloud_account_id
                   ORDER BY updated_at DESC NULLS LAST, id DESC
               ) AS rn
        FROM public.llm_conversation_feedback
    ) ranked
    WHERE rn > 1
);

ALTER TABLE "public"."llm_conversation_feedback"
  ADD CONSTRAINT "llm_conversation_feedback_session_conversation_user_account_key"
  UNIQUE (session_id, conversation_id, user_id, cloud_account_id);
