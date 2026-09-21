-- Dedup key for NULL user_id conversations (GH #37367): Postgres treats
-- NULL <> NULL, so the existing UNIQUE (session_id, user_id, account_id)
-- constraint never catches a second concurrent save for the same session_id.
--
-- Rows created before this migration already violate the new key, so the index
-- cannot be built until they are collapsed. The duplicates carry conversation
-- history (messages, agent steps, tool calls, token usage), and the foreign
-- keys are a mix of NO ACTION/RESTRICT (a plain DELETE errors) and CASCADE (a
-- plain DELETE destroys the rows silently), so the losers are reparented onto
-- the survivor rather than deleted outright.
--
-- Survivor = newest updated_at, matching the ORDER BY updated_at DESC that
-- GetConversationBySession and the llm-server resume check now use, so the
-- collapse keeps the same row those read paths would have picked.
--
-- No CONCURRENTLY (Atlas Community runs migrations in a transaction). Pre-apply
-- out-of-band on each live DB before merging:
--   CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS llm_conversations_session_account_null_user_uniq
--     ON public.llm_conversations (session_id, account_id) WHERE user_id IS NULL;
-- Skipping that means this statement takes an ACCESS EXCLUSIVE lock instead.

CREATE TEMP TABLE _v904_dupes ON COMMIT DROP AS
WITH ranked AS (
    SELECT id,
           first_value(id) OVER w AS winner_id,
           row_number()    OVER w AS rn
    FROM public.llm_conversations
    WHERE user_id IS NULL
    WINDOW w AS (
        PARTITION BY session_id, account_id
        ORDER BY updated_at DESC, created_at DESC, id DESC
    )
)
SELECT id AS loser_id, winner_id FROM ranked WHERE rn > 1;

CREATE INDEX ON _v904_dupes (loser_id);

-- llm_conversation_tool_calls is the one child with a unique key that spans
-- conversation_id, so a reparent can collide with a row the survivor already
-- has. Drop the losing copy; plain = matches the constraint's NULL semantics.
DELETE FROM public.llm_conversation_tool_calls t
USING _v904_dupes d
WHERE t.conversation_id = d.loser_id
  AND EXISTS (
      SELECT 1
      FROM public.llm_conversation_tool_calls w
      WHERE w.conversation_id = d.winner_id
        AND w.tool_name  = t.tool_name
        AND w.message_id = t.message_id
        AND w.tool_id    = t.tool_id
        AND w.agent_id   = t.agent_id
  );

UPDATE public.llm_conversation_agent x
SET conversation_id = d.winner_id FROM _v904_dupes d WHERE x.conversation_id = d.loser_id;

UPDATE public.llm_conversation_messages x
SET conversation_id = d.winner_id FROM _v904_dupes d WHERE x.conversation_id = d.loser_id;

UPDATE public.llm_conversation_saved x
SET conversation_id = d.winner_id FROM _v904_dupes d WHERE x.conversation_id = d.loser_id;

UPDATE public.llm_conversation_token_usage x
SET conversation_id = d.winner_id FROM _v904_dupes d WHERE x.conversation_id = d.loser_id;

UPDATE public.llm_conversation_tool_calls x
SET conversation_id = d.winner_id FROM _v904_dupes d WHERE x.conversation_id = d.loser_id;

UPDATE public.llm_conversation_agent_critiques x
SET conversation_id = d.winner_id FROM _v904_dupes d WHERE x.conversation_id = d.loser_id;

-- conversation_followup_decision has a CASCADE foreign key to llm_conversations
-- on dev, but no migration in this tree creates it (and no service code
-- references it) -- it is out-of-band schema drift. A fresh database therefore
-- has no such table, so the reparent has to be guarded or the fresh-DB apply
-- fails here. Guarded rather than dropped: wherever the table does exist, the
-- CASCADE would quietly take its rows with the losers.
DO $$
BEGIN
    IF to_regclass('public.conversation_followup_decision') IS NOT NULL THEN
        UPDATE public.conversation_followup_decision x
        SET conversation_id = d.winner_id FROM _v904_dupes d WHERE x.conversation_id = d.loser_id;
    END IF;
END $$;

UPDATE public.llm_watch_tasks x
SET conversation_id = d.winner_id FROM _v904_dupes d WHERE x.conversation_id = d.loser_id;

DELETE FROM public.llm_conversations c
USING _v904_dupes d
WHERE c.id = d.loser_id;

CREATE UNIQUE INDEX IF NOT EXISTS llm_conversations_session_account_null_user_uniq
  ON public.llm_conversations (session_id, account_id)
  WHERE user_id IS NULL;
