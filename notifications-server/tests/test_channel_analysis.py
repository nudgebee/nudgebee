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
