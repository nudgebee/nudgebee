import pytest

from notifications_server.services import channel_analysis as ca


class TestOverrides:
    @pytest.mark.parametrize(
        "text",
        [
            "forget that, what is the pod limit?",
            "Forget everything and start fresh",
            "let's start over",
            "ignore the context please",
        ],
    )
    def test_forget_phrases(self, text):
        assert ca.detect_overrides(text)["forget"] is True

    @pytest.mark.parametrize(
        "text",
        [
            "only this thread please",
            "just this thread",
            "stay in this thread",
        ],
    )
    def test_thread_only_phrases(self, text):
        assert ca.detect_overrides(text)["thread_only"] is True

    def test_ignore_last_answer(self):
        assert ca.detect_overrides("ignore your last answer and try again")["ignore_last"] is True

    def test_ordinary_question_sets_nothing(self):
        assert ca.detect_overrides("what broke the deploy?") == {
            "forget": False,
            "thread_only": False,
            "ignore_last": False,
        }

    def test_empty_text_is_safe(self):
        assert ca.detect_overrides(None)["forget"] is False


class TestSelfContainedGate:
    def test_general_question_with_no_overlap_needs_no_history(self):
        assert ca.is_self_contained("what is the default pod cpu limit?", ["lunch plans", "who is on call"]) is True

    def test_a_word_pointing_outside_the_question_keeps_context(self):
        assert ca.is_self_contained("why did that happen?", ["lunch plans"]) is False

    def test_shared_vocabulary_keeps_context(self):
        # Same question, but the room is already discussing pod limits.
        assert ca.is_self_contained("what is the default pod cpu limit?", ["raise the pod cpu limit"]) is False

    def test_empty_question_is_never_self_contained(self):
        assert ca.is_self_contained("   ", ["anything"]) is False

    def test_question_of_only_stopwords_is_never_self_contained(self):
        assert ca.is_self_contained("what is it", []) is False

    def test_bot_mention_alone_does_not_make_a_question(self):
        assert ca.is_self_contained("<@UBOT>", ["deploy failed"]) is False

    def test_none_surrounding_texts_are_tolerated(self):
        assert ca.is_self_contained("what is the default pod cpu limit?", [None, ""]) is True


class TestTags:
    @pytest.mark.parametrize(
        "text",
        ["decided: we ship friday", "let's go with option B", "approved", "we're going with the standby"],
    )
    def test_decision_markers(self, text):
        assert ca.classify_decision(text) is True

    def test_ordinary_message_is_not_a_decision(self):
        assert ca.classify_decision("what do you think about friday?") is False

    def test_topic_picks_the_dominant_bucket(self):
        assert ca.classify_topic("the rds replica failover needs a migration") == "database"

    def test_topic_is_none_when_nothing_matches(self):
        assert ca.classify_topic("lunch at one?") is None

    def test_extract_people_dedupes_and_keeps_order(self):
        assert ca.extract_people("<@U2> and <@U1> and <@U2> again") == ["U2", "U1"]

    def test_extract_people_handles_the_piped_form(self):
        assert ca.extract_people("<@U1|john> said so") == ["U1"]

    def test_extract_people_on_text_without_mentions(self):
        assert ca.extract_people("nobody here") == []

    def test_strip_user_mentions_removes_ids(self):
        assert "U1" not in ca.strip_user_mentions("<@U1> what broke?")


class TestLowSignal:
    @pytest.mark.parametrize(
        "text",
        [
            "hello, how are you?",
            "how are you doing today?",
            "how are things today?",
            "<@U05NY2LD3A8> hey",
            "thanks!",
            "good morning",
        ],
    )
    def test_pleasantries_are_low_signal(self, text):
        assert ca.is_low_signal(text)

    @pytest.mark.parametrize(
        "text",
        [
            "Seems like issue with ticket server",
            "there is some issue with deployment name app-dev",
            "<@U05NY2LD3A8> how is infra today? any issues?",
            "can you check workload issues?",
        ],
    )
    def test_messages_naming_a_problem_are_kept(self, text):
        assert not ca.is_low_signal(text)

    @pytest.mark.parametrize(
        "text",
        [
            "db is not good",
            "s3 is doing fine",
            "lb doing well today",
            "mq down",
        ],
    )
    def test_short_resource_names_survive_casual_phrasing(self, text):
        # Two-letter service names ("db", "s3", "lb", "mq") must not be filtered
        # out just because the rest of the message is casual — a status message
        # about them is real signal.
        assert not ca.is_low_signal(text)

    def test_resolver_that_raises_or_returns_none_falls_back_to_id(self):
        assert ca.replace_user_mentions("<@U1> ping", lambda uid: None) == "@U1 ping"

        def boom(uid):
            raise KeyError(uid)

        assert ca.replace_user_mentions("<@U1> ping", boom) == "@U1 ping"

    def test_drop_low_signal_filters_entries_in_order(self):
        entries = [
            {"message": "hello there"},
            {"message": "ticket server is down"},
            {"message": "how are you doing today?"},
            {"message": "app-dev deployment failing"},
        ]
        kept = [e["message"] for e in ca.drop_low_signal(entries)]
        assert kept == ["ticket server is down", "app-dev deployment failing"]


class TestReplaceUserMentions:
    def test_resolves_ids_to_display_names(self):
        out = ca.replace_user_mentions("<@U1> can you check with <@U2>?", {"U1": "Nubi", "U2": "Arjun"}.__getitem__)
        assert out == "@Nubi can you check with @Arjun?"

    def test_empty_text_is_safe(self):
        assert ca.replace_user_mentions("", lambda uid: uid) == ""
        assert ca.replace_user_mentions(None, lambda uid: uid) is None
