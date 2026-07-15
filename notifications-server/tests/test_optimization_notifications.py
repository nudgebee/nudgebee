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


def _flat(msg_or_blocks):
    """Accept a template result dict or a raw block list; items now ride in
    colored attachments, so text/button extraction walks both."""
    if isinstance(msg_or_blocks, dict):
        return list(msg_or_blocks.get("blocks", [])) + [
            b for a in msg_or_blocks.get("attachments", []) for b in a.get("blocks", [])
        ]
    return msg_or_blocks


def _slack_text(blocks) -> str:
    """Concatenate all rendered mrkdwn text in a Slack message or block list."""
    parts: List[str] = []
    for b in _flat(blocks):
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
        text = _slack_text(msg)

        assert "You can cut $3,840/mo across 1 account" in text
        assert "*In this brief:* 1 Priority · 2 Critical" in text
        assert "*New since yesterday:* 1 Priority · 3 Critical · 2 High" in text
        assert "*2 resolved* (saved $312.00/mo)" in text
        assert "195 still open from earlier" in text

    def test_new_counts_all_zero_renders_quiet_day(self):
        params = _params(
            new_counts=NewCounts(act_now=0, critical=0, high=0),
            resolved_count=0,
            carryover_count=247,
        )
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))

        assert "_No new recommendations since yesterday._" in text
        assert "247 still open from earlier" in text
        assert "New since yesterday: " not in text  # exact "no parts" branch

    def test_old_payload_omits_delta_lines(self):
        # No new_counts/resolved/carryover provided -> graceful degrade
        params = _params()  # leaves new_counts=None and all delta ints at 0
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))

        assert "You can cut $3,840/mo across 1 account" in text
        assert "New since yesterday" not in text
        assert "still open from earlier" not in text
        assert "resolved" not in text.lower() or "Resolved" not in text  # no resolved line at all

    def test_savings_zero_hides_recoverable_line(self):
        params = _params(
            total_recoverable_savings=0,
            new_counts=NewCounts(act_now=0, critical=3, high=0),
            carryover_count=247,
        )
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))

        assert "You can cut" not in text  # no savings headline on a $0 digest
        assert "*New since yesterday:* 3 Critical" in text

    def test_footer_url_has_digest_utm_and_date(self):
        params = _params(new_counts=NewCounts(act_now=1, critical=0, high=0))
        msg = get_recommendation_nudge_digest_message_template(params)
        # per-item Details buttons come first; the footer CTA is the last actions block
        action_block = [b for b in _flat(msg) if b.get("type") == "actions"][-1]
        url = action_block["elements"][0]["url"]
        assert "utm=slack-digest" in url
        assert "d=2026-05-18" in url

    def test_items_capped_at_three_with_overflow_count(self):
        recs = [
            DigestRecommendation(
                id=f"rec-{i}",
                rule_name="pod_right_sizing",
                resource_name=f"prod/Deployment/svc-{i}",
                finops_score=90 - i,
                finops_band="Act Now",
                estimated_savings=100.0 + i,
                cta_url=f"https://app/optimise?id=rec-{i}#recommendations",
            )
            for i in range(5)
        ]
        params = _params(
            recommendations_by_account={"acc-1": AccountRecommendations(account_name="prod-aws", recommendations=recs)}
        )
        msg = get_recommendation_nudge_digest_message_template(params)
        text = _slack_text(msg)

        assert "svc-0" in text and "svc-1" in text and "svc-2" in text
        assert "svc-3" not in text and "svc-4" not in text
        assert "+2 more in the dashboard" in text
        # highest-score item renders first, with facts and identity in a context line
        assert "Priority · Score 90/100" in text
        assert "Acct prod-aws" in text

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


# ---------------------------- Copy library + accrued waste ----------------------------


class TestItemCopyAndWaste:
    def test_rule_name_renders_curated_display_name(self):
        text = _slack_text(get_recommendation_nudge_digest_message_template(_params()))
        assert "Workload rightsizing" in text
        assert "Pod Right Sizing" not in text

    def test_waste_clause_renders_above_floor(self):
        params = _params()
        params.recommendations_by_account["acc-1"].recommendations[0].wasted_since_detected = 1120.0
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))
        assert "wasted since detected" in text
        assert "$1,120" in text

    def test_waste_clause_hidden_below_floor(self):
        params = _params()
        params.recommendations_by_account["acc-1"].recommendations[0].wasted_since_detected = 4.0
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))
        assert "wasted since detected" not in text

    def test_old_payload_without_waste_field_degrades(self):
        # producer payloads predating wasted_since_detected default to 0 -> no clause
        text = _slack_text(get_recommendation_nudge_digest_message_template(_params()))
        assert "wasted since detected" not in text


class TestPostureItemUrlAndHeadlineGuards:
    def test_relative_cta_url_resolved_against_base_url(self):
        params = _params()
        params.recommendations_by_account["acc-1"].recommendations[0].cta_url = "/optimise?id=rec-1#recommendations"
        msg = get_recommendation_nudge_digest_message_template(params)
        item_button = [b for b in _flat(msg) if b.get("type") == "actions"][0]
        assert item_button["elements"][0]["url"] == "https://app/optimise?id=rec-1#recommendations"

    def test_absolute_cta_url_untouched(self):
        msg = get_recommendation_nudge_digest_message_template(_params())
        item_button = [b for b in _flat(msg) if b.get("type") == "actions"][0]
        assert item_button["elements"][0]["url"].startswith("https://app/optimise?id=rec-1")

    def test_nudge_zero_savings_headline_falls_back_to_count(self):
        from notifications_server.message_templates.slack.recommendation_proactive_nudge import (
            ProactiveNudgeParams,
            get_recommendation_proactive_nudge_message_template,
        )

        rec = DigestRecommendation(
            id="rec-9",
            rule_name="popeye_misconfigurations",
            resource_name="prod/Deployment/api",
            finops_score=70,
            finops_band="Act Now",
            estimated_savings=0,
        )
        params = ProactiveNudgeParams(
            total_recommendations=1,
            total_recoverable_savings=0,
            recommendations_by_account={"acc-1": AccountRecommendations(account_name="prod", recommendations=[rec])},
            base_url="https://app",
        )
        text = _slack_text(get_recommendation_proactive_nudge_message_template(params))
        assert "You can cut" not in text
        assert "$0" not in text
        assert "1 priority recommendations need action across 1 account" in text


class TestItemTitleAndCategoryChips:
    def test_item_title_leads_with_rule_display_name(self):
        text = _slack_text(get_recommendation_nudge_digest_message_template(_params()))
        assert "*Workload rightsizing — prod/Deployment/payments-api*" in text

    def test_long_arm_id_resource_is_shortened(self):
        arm = (
            "/subscriptions/19e207a9-769d-4afd-b261-10bbed2d43e8/resourcegroups/nudgebee-dev_group"
            "/providers/microsoft.compute/disks/nudgebee-windows-vm_osdisk_1_05141b666c7840ceabb"
        )
        params = _params()
        params.recommendations_by_account["acc-1"].recommendations[0].resource_name = arm
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))
        assert "/subscriptions/" not in text
        assert "nudgebee-windows-vm_osdisk_1_05141b666c7840ceabb" in text

    def test_category_chips_render_sorted(self):
        params = _params(category_savings={"RightSizing": 1800.0, "Configuration": 760.0, "InfraUpgrade": 2100.0})
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))
        assert "*By category:* Infra upgrades $2,100 · Rightsizing $1,800 · Configuration $760.00" in text

    def test_no_category_chips_without_data(self):
        text = _slack_text(get_recommendation_nudge_digest_message_template(_params()))
        assert "By category" not in text
