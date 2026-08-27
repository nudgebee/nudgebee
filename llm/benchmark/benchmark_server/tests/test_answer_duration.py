"""``answer_duration_seconds`` — the one number every terminal test row carries.

The guard test always runs. The DB test runs only against a real
``APP_DATABASE_URL`` (dev/local); it pins the property that actually broke:
a followup wait must be *subtracted* from the generation span, not counted
as the agent's answer time.
"""

import os

import pytest
from sqlalchemy import text

from benchmark_server.utils.db_utils import db_engine
from benchmark_server.utils.run_manager import answer_duration_seconds

# conftest points APP_DATABASE_URL at an unreachable stub so unit tests never
# hit a database; only a real URL exported by the developer enables the second
# test below.
_STUB_DB = "postgresql://test:test@localhost:1/test"
_HAS_DB = db_engine is not None and os.environ.get("APP_DATABASE_URL") != _STUB_DB


@pytest.mark.parametrize("cid", ["", None, "not-a-uuid", 0, "66486e37-eb1e"])
def test_non_uuid_conversation_id_is_none(cid):
    """A blank id, or a session_id passed where a conversation UUID belongs,
    must return None (caller keeps whatever duration it had) rather than blow
    up a store_test_result write."""
    assert answer_duration_seconds(cid) is None


@pytest.mark.skipif(not _HAS_DB, reason="needs a real APP_DATABASE_URL")
def test_followup_wait_is_subtracted():
    with db_engine.connect() as conn:
        row = conn.execute(text("""
                SELECT g.conversation_id,
                       EXTRACT(EPOCH FROM (
                           MAX(COALESCE(g.responded_at, g.updated_at)) - MIN(g.created_at)
                       )) AS gen_span
                FROM llm_conversation_messages g
                WHERE g.message_type = 'generation'
                  AND EXISTS (
                      SELECT 1 FROM llm_conversation_messages f
                      WHERE f.conversation_id = g.conversation_id
                        AND f.message_type = 'followup'
                        AND f.responded_at IS NOT NULL
                        AND f.responded_at - f.created_at > interval '60 seconds'
                  )
                GROUP BY g.conversation_id
                LIMIT 1
            """)).fetchone()

    if not row:
        pytest.skip("no conversation with a long answered followup in this DB")

    conversation_id, gen_span = row[0], float(row[1])
    measured = answer_duration_seconds(conversation_id)

    assert measured is not None
    assert 0 <= measured < gen_span - 60, (
        f"followup wait not subtracted for {conversation_id}: "
        f"measured={measured} generation span={gen_span}"
    )
