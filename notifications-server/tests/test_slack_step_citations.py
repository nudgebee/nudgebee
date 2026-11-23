"""Step citations ([Tool - E3](#task-<step id>)) in investigation text posted to Slack."""

import pytest

from notifications_server.configs.settings import settings
from notifications_server.message_templates.slack.generic import (
    MAX_SLACK_BLOCK_LENGTH,
    GenericMessageParams,
    WorkflowMetadata,
    _chunk_into_sections,
    _inside_link,
    get_slack_generic_message_template,
)
from notifications_server.utils.transformer import Transformer

STEP = "58033b0e-8c8e-4d15-a337-64153df19136"
CITED = f"has 592 ready messages [Shell Execute - E13](#task-{STEP}), above the threshold"
PAGE = "https://app.example.com/investigate?accountId=acc-1&id=evt-1&utm=slack"


@pytest.fixture
def base_url(monkeypatch):
    monkeypatch.setattr(settings, "base_url", "https://app.example.com")
    monkeypatch.setattr(settings.urls, "base_url", "https://app.example.com")


class TestStepCitationsToSlack:
    def test_links_to_the_step_on_the_event_page(self):
        out = Transformer.step_citations_to_slack(CITED, PAGE)
        assert out == f"has 592 ready messages <{PAGE}#task-{STEP}|Shell Execute - E13>, above the threshold"

    def test_without_a_page_keeps_only_the_label(self):
        out = Transformer.step_citations_to_slack(CITED)
        assert out == "has 592 ready messages [Shell Execute - E13], above the threshold"

    def test_relative_page_is_not_linked(self):
        out = Transformer.step_citations_to_slack(CITED, "/investigate?id=evt-1")
        assert "#task-" not in out
        assert "[Shell Execute - E13]" in out

    def test_label_cannot_break_the_slack_link(self):
        out = Transformer.step_citations_to_slack(f"[a|b>c](#task-{STEP})", PAGE)
        assert out == f"<{PAGE}#task-{STEP}|abc>"

    def test_other_links_are_left_alone(self):
        text = "see [PR 12](https://github.com/example/repo/pull/12)"
        assert Transformer.step_citations_to_slack(text, PAGE) == text

    def test_markdown_conversion_applies_it_outside_code_fences(self):
        fenced = f"```\n[Shell Execute - E13](#task-{STEP})\n```"
        out = Transformer.markdown_to_slack_markdown(f"**Hurting now:** {CITED}\n{fenced}", PAGE)
        assert f"<{PAGE}#task-{STEP}|Shell Execute - E13>" in out
        assert fenced in out

    def test_markdown_conversion_without_page_drops_the_anchor(self):
        out = Transformer.markdown_to_slack_markdown(CITED)
        assert "#task-" not in out


class TestGenericSlackMessage:
    def _body_text(self, template):
        return "\n".join(b["text"]["text"] for b in template["blocks"] if b.get("type") == "section")

    def test_event_triggered_run_links_citations(self, base_url):
        params = GenericMessageParams(
            message=CITED,
            workflow_metadata=WorkflowMetadata(workflow_name="wf", account_id="acc-1", event_id="evt-1"),
        )
        text = self._body_text(get_slack_generic_message_template(params))
        assert f"<{PAGE}#task-{STEP}|Shell Execute - E13>" in text

    def test_run_without_event_keeps_labels(self, base_url):
        params = GenericMessageParams(message=CITED, workflow_metadata=WorkflowMetadata(workflow_name="wf"))
        text = self._body_text(get_slack_generic_message_template(params))
        assert "[Shell Execute - E13]" in text
        assert "#task-" not in text


class TestChunkIntoSections:
    def test_never_cuts_inside_a_link(self):
        link = f"<{PAGE}#task-{STEP}|Shell Execute - E22>"
        filler = "word " * ((MAX_SLACK_BLOCK_LENGTH - 40) // 5)
        sections = _chunk_into_sections(filler + link + " tail")
        texts = [s["text"]["text"] for s in sections]
        assert all(len(t) <= MAX_SLACK_BLOCK_LENGTH for t in texts)
        assert any(link in t for t in texts)

    def test_prefers_line_breaks(self):
        first = "a" * (MAX_SLACK_BLOCK_LENGTH - 10)
        sections = _chunk_into_sections(f"{first}\nsecond line with words")
        assert [s["text"]["text"] for s in sections] == [first, "second line with words"]

    def test_unbreakable_text_is_still_split(self):
        sections = _chunk_into_sections("x" * (MAX_SLACK_BLOCK_LENGTH + 5))
        assert [len(s["text"]["text"]) for s in sections] == [MAX_SLACK_BLOCK_LENGTH, 5]

    def test_a_stray_less_than_is_text_not_a_link(self):
        sections = _chunk_into_sections("latency < 200 ms " + "word " * 1000)
        texts = [s["text"]["text"] for s in sections]
        assert all(len(t) <= MAX_SLACK_BLOCK_LENGTH for t in texts)
        assert all(t.endswith("word") or t.endswith("ms") for t in texts)

    def test_a_less_than_on_an_earlier_line_never_opens_a_link(self):
        text = "a < b\nsee <https://example.com|this> now"
        cut = text.index("see") + 3
        assert _inside_link(text, cut) is False
        assert _inside_link(text, text.index("this") + 1) is True
