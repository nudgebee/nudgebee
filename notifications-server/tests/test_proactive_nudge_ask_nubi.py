"""
Tests for the "Ask Nubi" button destination in the FinOps Alert proactive-nudge
notification (Slack / MS Teams / Google Chat).

Regression guard for #31262: the button used to link to ``/chat`` (a leftover
Socket.IO echo-demo page) instead of the Nubi assistant. It must point at
``/ask-nudgebee``, scoped to ``accountId`` when the bundle covers one account.
"""

from typing import Any, Dict, List

from notifications_server.message_templates.google_chat.recommendation_proactive_nudge import (
    get_gchat_recommendation_proactive_nudge_template,
)
from notifications_server.message_templates.ms_teams.recommendation_proactive_nudge import (
    get_teams_recommendation_proactive_nudge_template,
)
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    AccountRecommendations,
    DigestRecommendation,
)
from notifications_server.message_templates.slack.recommendation_proactive_nudge import (
    ProactiveNudgeParams,
    build_ask_nubi_url,
    get_recommendation_proactive_nudge_message_template,
)


def _rec(rec_id: str) -> DigestRecommendation:
    return DigestRecommendation(
        id=rec_id,
        rule_name="pod_right_sizing",
        resource_name="prod/Deployment/payments-api",
        finops_score=85,
        finops_band="Act Now",
        estimated_savings=184.0,
        severity="High",
        category="RightSizing",
    )


def _params(accounts: Dict[str, str]) -> ProactiveNudgeParams:
    """Build params from an {account_id: account_name} map."""
    by_account = {
        acc_id: AccountRecommendations(account_name=name, recommendations=[_rec(f"rec-{i}")])
        for i, (acc_id, name) in enumerate(accounts.items())
    }
    return ProactiveNudgeParams(
        organization_id="org-1",
        organization_name="TestOrg",
        total_recommendations=len(by_account),
        total_recoverable_savings=184.0,
        recommendations_by_account=by_account,
        base_url="https://app",
    )


def _slack_ask_nubi_url(blocks: List[Dict[str, Any]]) -> str:
    for b in blocks:
        for e in b.get("elements", []) or []:
            text = e.get("text", {})
            if isinstance(text, dict) and text.get("text") == "Ask Nubi":
                return e["url"]
    raise AssertionError("Ask Nubi button not found in Slack blocks")


def _teams_ask_nubi_url(card: Dict[str, Any]) -> str:
    for action in card.get("actions", []):
        if action.get("title") == "Ask Nubi":
            return action["url"]
    raise AssertionError("Ask Nubi action not found in Teams card")


def _gchat_ask_nubi_url(text: str) -> str:
    for line in text.splitlines():
        if line.startswith("Ask Nubi:"):
            return line.split("Ask Nubi:", 1)[1].strip()
    raise AssertionError("Ask Nubi line not found in Google Chat text")


# --- helper unit tests -------------------------------------------------------


def test_helper_points_at_ask_nudgebee_not_chat():
    url = build_ask_nubi_url(_params({"acc-1": "prod-aws"}), "https://app", "slack")
    assert url.startswith("https://app/ask-nudgebee?")
    assert "/chat" not in url


def test_helper_scopes_account_when_single():
    url = build_ask_nubi_url(_params({"acc-1": "prod-aws"}), "https://app", "slack")
    assert "accountId=acc-1" in url
    assert "utm=slack" in url


def test_helper_omits_account_when_multiple():
    url = build_ask_nubi_url(_params({"acc-1": "prod-aws", "acc-2": "stage-aws"}), "https://app", "teams")
    assert "accountId=" not in url
    assert url == "https://app/ask-nudgebee?utm=teams"


# --- per-channel rendering tests --------------------------------------------


def test_slack_button_links_to_nubi_scoped():
    out = get_recommendation_proactive_nudge_message_template(_params({"acc-1": "prod-aws"}))
    url = _slack_ask_nubi_url(out["blocks"])
    assert url == "https://app/ask-nudgebee?utm=slack&accountId=acc-1"


def test_teams_button_links_to_nubi_scoped():
    out = get_teams_recommendation_proactive_nudge_template(_params({"acc-1": "prod-aws"}))
    url = _teams_ask_nubi_url(out)
    assert url == "https://app/ask-nudgebee?utm=teams&accountId=acc-1"


def test_gchat_line_links_to_nubi_scoped():
    out = get_gchat_recommendation_proactive_nudge_template(_params({"acc-1": "prod-aws"}))
    url = _gchat_ask_nubi_url(out["text"])
    assert url == "https://app/ask-nudgebee?utm=gchat&accountId=acc-1"


def test_no_channel_links_to_dead_chat_page():
    params = _params({"acc-1": "prod-aws", "acc-2": "stage-aws"})
    slack = get_recommendation_proactive_nudge_message_template(params)
    teams = get_teams_recommendation_proactive_nudge_template(params)
    gchat = get_gchat_recommendation_proactive_nudge_template(params)
    assert "/chat" not in _slack_ask_nubi_url(slack["blocks"])
    assert "/chat" not in _teams_ask_nubi_url(teams)
    assert "/chat" not in _gchat_ask_nubi_url(gchat["text"])
