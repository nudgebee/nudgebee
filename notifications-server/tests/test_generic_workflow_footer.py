"""
Tests for the workflow tracing footer added to generic notifications.

Covers Slack, MS Teams, and Google Chat templates and the shared
GenericMessageParams model parsing.
"""

import pytest

from notifications_server.configs.settings import settings
from notifications_server.message_templates.google_chat.generic import (
    get_google_chat_generic_message_template,
)
from notifications_server.message_templates.ms_teams.generic import (
    get_ms_teams_generic_message_template,
)
from notifications_server.message_templates.slack.generic import (
    GenericMessageParams,
    MAX_BLOCKS,
    MAX_SLACK_BLOCK_LENGTH,
    WORKFLOW_FOOTER_BLOCKS_RESERVED,
    WorkflowMetadata,
    get_generic_message_params,
    get_slack_generic_message_template,
)


def _params(**kwargs):
    return GenericMessageParams(message="hello", **kwargs)


def _meta(**kwargs):
    return WorkflowMetadata(**kwargs)


@pytest.fixture
def base_url(monkeypatch):
    """Set a stable base_url so link assertions are deterministic."""
    monkeypatch.setattr(settings, "base_url", "https://app.test")
    monkeypatch.setattr(settings.urls, "base_url", "https://app.test")
    return "https://app.test"


# ---------------------------- Slack ----------------------------


class TestSlackGenericFooter:
    def test_no_metadata_renders_no_footer(self):
        out = get_slack_generic_message_template(_params())
        assert all(b.get("type") == "section" for b in out["blocks"])

    def test_metadata_with_workflow_and_user_renders_footer(self):
        params = _params(
            workflow_metadata=_meta(workflow_name="MyWorkflow", triggered_by="Alice"),
        )
        blocks = get_slack_generic_message_template(params)["blocks"]
        assert blocks[-2] == {"type": "divider"}
        assert blocks[-1]["type"] == "context"
        text = blocks[-1]["elements"][0]["text"]
        assert "Automation: *MyWorkflow*" in text
        assert "Triggered by: Alice" in text

    def test_metadata_with_workflow_only_omits_triggered_by(self):
        params = _params(workflow_metadata=_meta(workflow_name="OnlyWorkflow"))
        text = get_slack_generic_message_template(params)["blocks"][-1]["elements"][0]["text"]
        assert "Automation: *OnlyWorkflow*" in text
        assert "Triggered by" not in text

    def test_metadata_without_workflow_name_renders_no_footer(self):
        params = _params(workflow_metadata=_meta(triggered_by="Alice"))
        blocks = get_slack_generic_message_template(params)["blocks"]
        assert all(b.get("type") == "section" for b in blocks)

    def test_workflow_id_and_account_id_renders_link(self, base_url):
        params = _params(
            workflow_metadata=_meta(
                workflow_name="cleanup-old-instances",
                workflow_id="wf-123",
                account_id="acct-456",
            ),
        )
        text = get_slack_generic_message_template(params)["blocks"][-1]["elements"][0]["text"]
        assert (
            "Automation: <https://app.test/workflow/wf-123?accountId=acct-456&utm=slack|*cleanup-old-instances*>"
            in text
        )

    def test_workflow_id_without_account_id_still_renders_link(self, base_url):
        params = _params(
            workflow_metadata=_meta(workflow_name="W", workflow_id="wf-1"),
        )
        text = get_slack_generic_message_template(params)["blocks"][-1]["elements"][0]["text"]
        assert "Automation: <https://app.test/workflow/wf-1?utm=slack|*W*>" in text

    def test_missing_base_url_falls_back_to_plaintext(self, monkeypatch):
        monkeypatch.setattr(settings, "base_url", "")
        params = _params(
            workflow_metadata=_meta(workflow_name="W", workflow_id="wf-1", account_id="acct-1"),
        )
        text = get_slack_generic_message_template(params)["blocks"][-1]["elements"][0]["text"]
        assert "Automation: *W*" in text
        assert "<http" not in text

    def test_block_limit_respects_footer_reservation(self):
        # Build a message that would otherwise produce more than MAX_BLOCKS sections
        long_message = "x" * (MAX_SLACK_BLOCK_LENGTH * (MAX_BLOCKS + 5))
        params = GenericMessageParams(
            message=long_message,
            workflow_metadata=_meta(workflow_name="W", triggered_by="U"),
        )
        blocks = get_slack_generic_message_template(params)["blocks"]
        # With footer (2 blocks reserved), section count is reduced
        assert len(blocks) <= MAX_BLOCKS
        assert blocks[-2] == {"type": "divider"}
        assert blocks[-1]["type"] == "context"

    def test_block_limit_without_footer_uses_full_capacity(self):
        long_message = "x" * (MAX_SLACK_BLOCK_LENGTH * (MAX_BLOCKS + 5))
        blocks = get_slack_generic_message_template(GenericMessageParams(message=long_message))["blocks"]
        section_count = sum(1 for b in blocks if b.get("type") == "section")
        assert section_count == MAX_BLOCKS


# ---------------------------- MS Teams ----------------------------


class TestMsTeamsGenericFooter:
    def test_no_metadata_renders_no_footer(self):
        card = get_ms_teams_generic_message_template(_params())
        assert len(card["body"]) == 1
        assert card["body"][0]["text"] == "hello"

    def test_metadata_appends_small_light_separator_block(self):
        params = _params(
            workflow_metadata=_meta(workflow_name="MyWorkflow", triggered_by="Alice"),
        )
        card = get_ms_teams_generic_message_template(params)
        footer = card["body"][-1]
        assert footer["type"] == "TextBlock"
        assert footer["size"] == "Small"
        assert footer["color"] == "Light"
        assert footer["separator"] is True
        assert "Automation: **MyWorkflow**" in footer["text"]
        assert "Triggered by: Alice" in footer["text"]

    def test_metadata_with_workflow_only(self):
        params = _params(workflow_metadata=_meta(workflow_name="OnlyWorkflow"))
        card = get_ms_teams_generic_message_template(params)
        text = card["body"][-1]["text"]
        assert "Automation: **OnlyWorkflow**" in text
        assert "Triggered by" not in text

    def test_workflow_id_and_account_id_renders_markdown_link(self, base_url):
        params = _params(
            workflow_metadata=_meta(
                workflow_name="cleanup-old-instances",
                workflow_id="wf-123",
                account_id="acct-456",
            ),
        )
        text = get_ms_teams_generic_message_template(params)["body"][-1]["text"]
        assert (
            "Automation: [**cleanup-old-instances**]"
            "(https://app.test/workflow/wf-123?accountId=acct-456&utm=teams)" in text
        )


# ---------------------------- Google Chat ----------------------------


class TestGoogleChatGenericFooter:
    def test_no_metadata_returns_plain_message(self):
        assert get_google_chat_generic_message_template(_params()) == "hello"

    def test_metadata_appends_text_footer(self):
        params = _params(
            workflow_metadata=_meta(workflow_name="MyWorkflow", triggered_by="Alice"),
        )
        out = get_google_chat_generic_message_template(params)
        assert out.startswith("hello\n---\n")
        assert "Automation: MyWorkflow" in out
        assert "Triggered by: Alice" in out

    def test_metadata_with_workflow_only(self):
        params = _params(workflow_metadata=_meta(workflow_name="OnlyWorkflow"))
        out = get_google_chat_generic_message_template(params)
        assert "Automation: OnlyWorkflow" in out
        assert "Triggered by" not in out

    def test_workflow_id_and_account_id_renders_link(self, base_url):
        params = _params(
            workflow_metadata=_meta(
                workflow_name="cleanup-old-instances",
                workflow_id="wf-123",
                account_id="acct-456",
            ),
        )
        out = get_google_chat_generic_message_template(params)
        assert (
            "Automation: <https://app.test/workflow/wf-123?accountId=acct-456&utm=gchat|cleanup-old-instances>" in out
        )


# ---------------------------- Param parsing ----------------------------


class TestGenericMessageParamsParsing:
    def test_workflow_metadata_dict_is_parsed(self):
        parsed = get_generic_message_params(
            message="hi",
            workflow_metadata={
                "workflow_name": "W",
                "triggered_by": "U",
                "workflow_id": "wf-1",
                "account_id": "acct-1",
            },
        )
        assert isinstance(parsed.workflow_metadata, WorkflowMetadata)
        assert parsed.workflow_metadata.workflow_name == "W"
        assert parsed.workflow_metadata.triggered_by == "U"
        assert parsed.workflow_metadata.workflow_id == "wf-1"
        assert parsed.workflow_metadata.account_id == "acct-1"

    def test_missing_workflow_metadata_is_none(self):
        parsed = get_generic_message_params(message="hi")
        assert parsed.workflow_metadata is None

    def test_workflow_metadata_legacy_dict_still_parses(self):
        # Legacy producers may only send workflow_name + triggered_by — must still parse.
        parsed = get_generic_message_params(
            message="hi",
            workflow_metadata={"workflow_name": "W", "triggered_by": "U"},
        )
        assert parsed.workflow_metadata.workflow_id is None
        assert parsed.workflow_metadata.account_id is None

    def test_workflow_footer_reservation_constant_is_two(self):
        # Sanity: divider + context = 2 blocks
        assert WORKFLOW_FOOTER_BLOCKS_RESERVED == 2
