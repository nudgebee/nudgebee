from datetime import datetime, timedelta

from notifications_server.services import channel_config, channel_ranking

NOW = datetime(2026, 7, 26, 12, 0)


def _config(**overrides):
    return channel_config.resolve(overrides)


def _entry(mid, minutes_ago, message="hello", decision=False, replies=0, thread=None):
    return {
        "provider_message_id": mid,
        "message": message,
        "posted_at": NOW - timedelta(minutes=minutes_ago),
        "is_decision": decision,
        "reply_count": replies,
        "thread_id": thread,
    }


def _ids(entries):
    return [entry["provider_message_id"] for entry in entries]


class TestScoring:
    def test_newer_outranks_older_all_else_equal(self):
        entries = channel_ranking.score_messages([_entry("old", 120), _entry("new", 1)], NOW, _config())
        scores = {entry["provider_message_id"]: entry["_score"] for entry in entries}
        assert scores["new"] > scores["old"]

    def test_recency_halves_every_halflife(self):
        config = _config(recency_halflife_minutes=30, rank_weight_salience=0.0)
        entries = channel_ranking.score_messages([_entry("a", 0), _entry("b", 30), _entry("c", 60)], NOW, config)
        a, b, c = (entry["_score"] for entry in entries)
        assert a == 1.0
        assert b == 0.5
        assert c == 0.25

    def test_a_decision_outranks_a_newer_plain_message(self):
        config = _config(recency_halflife_minutes=30, salience_weight_decision=2.0, rank_weight_salience=2.0)
        entries = channel_ranking.score_messages([_entry("plain", 5), _entry("call", 20, decision=True)], NOW, config)
        scores = {entry["provider_message_id"]: entry["_score"] for entry in entries}
        assert scores["call"] > scores["plain"]

    def test_replies_raise_salience(self):
        config = _config(rank_weight_recency=0.0)
        entries = channel_ranking.score_messages([_entry("quiet", 5), _entry("busy", 5, replies=6)], NOW, config)
        scores = {entry["provider_message_id"]: entry["_score"] for entry in entries}
        assert scores["busy"] > scores["quiet"]

    def test_identical_candidates_do_not_divide_by_zero(self):
        entries = channel_ranking.score_messages([_entry("a", 5), _entry("b", 5)], NOW, _config())
        assert all(entry["_score"] == entries[0]["_score"] for entry in entries)

    def test_missing_timestamp_is_treated_as_now_not_a_crash(self):
        entry = _entry("x", 0)
        entry["posted_at"] = None
        assert channel_ranking.score_messages([entry], NOW, _config())[0]["_score"] >= 0


class TestCap:
    def test_under_the_cap_everything_survives_in_order(self):
        entries = [_entry("b", 5), _entry("a", 10)]
        assert _ids(channel_ranking.apply_cap(entries, 10)) == ["a", "b"]

    def test_cap_keeps_the_highest_scoring(self):
        entries = channel_ranking.score_messages(
            [_entry("old", 600), _entry("mid", 60), _entry("new", 1)], NOW, _config()
        )
        assert _ids(channel_ranking.apply_cap(entries, 2)) == ["mid", "new"]

    def test_decisions_survive_a_cap_that_would_drop_them(self):
        entries = channel_ranking.score_messages(
            [_entry("call", 900, decision=True)] + [_entry(f"chat{i}", i) for i in range(5)], NOW, _config()
        )
        kept = _ids(channel_ranking.apply_cap(entries, 2))
        assert "call" in kept, "a settled decision must not be dropped by the message cap"

    def test_output_is_chronological_so_the_transcript_reads_correctly(self):
        entries = channel_ranking.score_messages(
            [_entry("new", 1), _entry("old", 50), _entry("mid", 20)], NOW, _config()
        )
        assert _ids(channel_ranking.apply_cap(entries, 3)) == ["old", "mid", "new"]

    def test_more_decisions_than_the_cap_still_returns_them(self):
        entries = channel_ranking.score_messages([_entry(f"d{i}", i, decision=True) for i in range(4)], NOW, _config())
        assert len(channel_ranking.apply_cap(entries, 2)) == 4
