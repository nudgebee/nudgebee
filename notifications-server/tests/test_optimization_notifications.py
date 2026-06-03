"""
Tests for the deltas-based daily FinOps digest (`recommendation_nudge_digest`).

Covers three header states across Slack, MS Teams, and Google Chat:
1. Deltas populated  -> NEW + Resolved + Carryover lines render
2. new_counts present but all zero -> "No new recommendations" placeholder
3. Delta fields absent (old-producer payload) -> graceful degrade, no delta lines
"""

import json
from typing import Any, Dict, List

from notifications_server.message_templates.google_chat.recommendation_nudge_digest import (
    get_gchat_recommendation_nudge_digest_template,
)
from notifications_server.message_templates.ms_teams.recommendation_nudge_digest import (
    get_teams_recommendation_nudge_digest_template,
)
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    AccountRecommendations,
    DigestRecommendation,
    NewCounts,
    RecommendationNudgeDigestParams,
    get_recommendation_nudge_digest_message_template,
)


def _params(**overrides) -> RecommendationNudgeDigestParams:
    base = {
        "organization_id": "org-1",
        "organization_name": "TestOrg",
        "total_recoverable_savings": 3840.0,
        "act_now_count": 1,
        "critical_count": 2,
        "high_count": 0,
        "recommendations_by_account": {
            "acc-1": AccountRecommendations(
                account_name="prod-aws",
                recommendations=[
                    DigestRecommendation(
                        id="rec-1",
                        rule_name="pod_right_sizing",
                        resource_name="prod/Deployment/payments-api",
                        finops_score=85,
                        finops_band="Act Now",
                        estimated_savings=184.0,
                        severity="High",
                        category="RightSizing",
                        cta_url="https://app/optimise?id=rec-1&utm=digest&d=2026-05-18#summary",
                    ),
                ],
            ),
        },
        "base_url": "https://app",
        "digest_date": "2026-05-18",
    }
    base.update(overrides)
    return RecommendationNudgeDigestParams(**base)


def _slack_text(blocks: List[Dict[str, Any]]) -> str:
    """Concatenate all rendered mrkdwn text in a Slack block list."""
    parts: List[str] = []
    for b in blocks:
        if "text" in b and isinstance(b["text"], dict) and "text" in b["text"]:
            parts.append(b["text"]["text"])
        for f in b.get("fields", []) or []:
            if isinstance(f, dict) and "text" in f:
                parts.append(f["text"])
        for e in b.get("elements", []) or []:
            if isinstance(e, dict) and "text" in e:
                txt = e["text"]
                if isinstance(txt, dict):
                    parts.append(txt.get("text", ""))
                else:
                    parts.append(txt)
    return "\n".join(parts)


def _teams_text(card: Dict[str, Any]) -> str:
    """Concatenate all TextBlock text + FactSet facts in an Adaptive Card."""
    parts: List[str] = []
    for item in card.get("body", []):
        if item.get("type") == "TextBlock":
            parts.append(item.get("text", ""))
        elif item.get("type") == "FactSet":
            for f in item.get("facts", []):
                parts.append(f"{f['title']}: {f['value']}")
    return "\n".join(parts)


# ---------------------------- Slack ----------------------------


class TestSlackDeltas:
    def test_deltas_populated_renders_three_lines(self):
        params = _params(
            new_counts=NewCounts(act_now=1, critical=3, high=2),
            resolved_count=2,
            resolved_savings=312.0,
            carryover_count=195,
        )
        msg = get_recommendation_nudge_digest_message_template(params)
        text = _slack_text(msg["blocks"])

        assert "$3,840/mo recoverable" in text
        assert "*New since yesterday:* 1 Priority · 3 Critical · 2 High" in text
        assert "*2 resolved* (saved $312.00/mo)" in text
        assert "195 still open from earlier" in text
        # Legacy `5 Critical · 12 High · 3 Priority` totals are gone
        assert "1 Priority · 2 Critical" not in text  # old act_now/critical/high path

    def test_new_counts_all_zero_renders_quiet_day(self):
        params = _params(
            new_counts=NewCounts(act_now=0, critical=0, high=0),
            resolved_count=0,
            carryover_count=247,
        )
        text = _slack_text(get_recommendation_nudge_digest_message_template(params)["blocks"])

        assert "_No new recommendations since yesterday._" in text
        assert "247 still open from earlier" in text
        assert "New since yesterday: " not in text  # exact "no parts" branch

    def test_old_payload_omits_delta_lines(self):
        # No new_counts/resolved/carryover provided -> graceful degrade
        params = _params()  # leaves new_counts=None and all delta ints at 0
        text = _slack_text(get_recommendation_nudge_digest_message_template(params)["blocks"])

        assert "$3,840/mo recoverable" in text
        assert "New since yesterday" not in text
        assert "still open from earlier" not in text
        assert "resolved" not in text.lower() or "Resolved" not in text  # no resolved line at all

    def test_savings_zero_hides_recoverable_line(self):
        params = _params(
            total_recoverable_savings=0,
            new_counts=NewCounts(act_now=0, critical=3, high=0),
            carryover_count=247,
        )
        text = _slack_text(get_recommendation_nudge_digest_message_template(params)["blocks"])

        assert "recoverable" not in text
        assert "*New since yesterday:* 3 Critical" in text

    def test_footer_url_has_digest_utm_and_date(self):
        params = _params(new_counts=NewCounts(act_now=1, critical=0, high=0))
        blocks = get_recommendation_nudge_digest_message_template(params)["blocks"]
        action_block = next(b for b in blocks if b.get("type") == "actions")
        url = action_block["elements"][0]["url"]
        assert "utm=slack-digest" in url
        assert "d=2026-05-18" in url

    def test_block_count_within_slack_limit(self):
        # Even with deltas + a recommendation, we stay well under Slack's 50.
        params = _params(
            new_counts=NewCounts(act_now=5, critical=5, high=5),
            resolved_count=10,
            resolved_savings=999.0,
            carryover_count=300,
        )
        blocks = get_recommendation_nudge_digest_message_template(params)["blocks"]
        assert len(blocks) <= 50


# ---------------------------- MS Teams ----------------------------


class TestTeamsDeltas:
    def test_deltas_populated_renders_factset(self):
        params = _params(
            new_counts=NewCounts(act_now=1, critical=3, high=2),
            resolved_count=2,
            resolved_savings=312.0,
            carryover_count=195,
        )
        text = _teams_text(get_teams_recommendation_nudge_digest_template(params))

        assert "Recoverable: $3,840/mo" in text
        assert "New since yesterday: 1 Priority · 3 Critical · 2 High" in text
        assert "Resolved: 2 (saved $312.00/mo)" in text
        assert "Still open from earlier: 195" in text

    def test_new_counts_all_zero_shows_none(self):
        params = _params(
            new_counts=NewCounts(act_now=0, critical=0, high=0),
            carryover_count=247,
        )
        text = _teams_text(get_teams_recommendation_nudge_digest_template(params))
        assert "New since yesterday: None" in text

    def test_old_payload_omits_delta_facts(self):
        text = _teams_text(get_teams_recommendation_nudge_digest_template(_params()))
        assert "Recoverable" in text  # savings > 0 still rendered
        assert "New since yesterday" not in text
        assert "Resolved" not in text

    def test_savings_zero_hides_recoverable_fact(self):
        params = _params(
            total_recoverable_savings=0,
            new_counts=NewCounts(act_now=0, critical=3, high=0),
            carryover_count=247,
        )
        text = _teams_text(get_teams_recommendation_nudge_digest_template(params))
        assert "Recoverable" not in text
        assert "New since yesterday: 3 Critical" in text

    def test_footer_url_has_digest_utm_and_date(self):
        params = _params(new_counts=NewCounts(act_now=1, critical=0, high=0))
        card = get_teams_recommendation_nudge_digest_template(params)
        url = card["actions"][0]["url"]
        assert "utm=teams-digest" in url
        assert "d=2026-05-18" in url


# ---------------------------- Google Chat ----------------------------


class TestGoogleChatDeltas:
    def test_deltas_populated_renders_three_lines(self):
        params = _params(
            new_counts=NewCounts(act_now=1, critical=3, high=2),
            resolved_count=2,
            resolved_savings=312.0,
            carryover_count=195,
        )
        text = get_gchat_recommendation_nudge_digest_template(params)["text"]

        assert "*$3,840/mo recoverable*" in text
        assert "*New since yesterday:* 1 Priority · 3 Critical · 2 High" in text
        assert "*2 resolved* (saved $312.00/mo)" in text
        assert "195 still open from earlier" in text

    def test_new_counts_all_zero_renders_quiet_day(self):
        params = _params(
            new_counts=NewCounts(act_now=0, critical=0, high=0),
            carryover_count=247,
        )
        text = get_gchat_recommendation_nudge_digest_template(params)["text"]
        assert "_No new recommendations since yesterday._" in text
        assert "247 still open from earlier" in text

    def test_old_payload_omits_delta_lines(self):
        text = get_gchat_recommendation_nudge_digest_template(_params())["text"]
        assert "New since yesterday" not in text
        assert "still open from earlier" not in text

    def test_savings_zero_hides_recoverable_line(self):
        params = _params(
            total_recoverable_savings=0,
            new_counts=NewCounts(act_now=0, critical=3, high=0),
            carryover_count=247,
        )
        text = get_gchat_recommendation_nudge_digest_template(params)["text"]
        assert "recoverable" not in text
        assert "*New since yesterday:* 3 Critical" in text

    def test_footer_url_has_digest_utm_and_date(self):
        params = _params(new_counts=NewCounts(act_now=1, critical=0, high=0))
        text = get_gchat_recommendation_nudge_digest_template(params)["text"]
        assert "utm=gchat-digest" in text
        assert "d=2026-05-18" in text


# ---------------------------- Payload-shape sanity ----------------------------


class TestPayloadParsing:
    def test_new_counts_dict_is_parsed(self):
        # Producer emits new_counts as a JSON dict; Pydantic should coerce
        # automatically into the NewCounts submodel.
        raw_json = json.dumps(
            {
                "organization_id": "x",
                "title": "T",
                "total_recoverable_savings": 100.0,
                "act_now_count": 0,
                "critical_count": 0,
                "high_count": 0,
                "recommendations_by_account": {},
                "base_url": "https://x",
                "new_counts": {"act_now": 2, "critical": 5, "high": 1},
                "resolved_count": 3,
                "resolved_savings": 42.0,
                "carryover_count": 99,
                "delta_window_hours": 24,
                "digest_date": "2026-05-18",
            }
        )
        parsed = RecommendationNudgeDigestParams(**json.loads(raw_json))
        assert isinstance(parsed.new_counts, NewCounts)
        assert parsed.new_counts.act_now == 2
        assert parsed.carryover_count == 99
        assert parsed.digest_date == "2026-05-18"
