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


def _attachments(msg) -> List[Dict[str, Any]]:
    return msg.get("attachments", []) if isinstance(msg, dict) else []


def _slack_text(msg_or_blocks) -> str:
    """Concatenate rendered mrkdwn from top-level blocks plus legacy
    attachment text/footer strings (items ride legacy attachments — Block Kit
    blocks inside attachments make Slack stamp an "Added by {app}" byline)."""
    parts: List[str] = []
    blocks = msg_or_blocks.get("blocks", []) if isinstance(msg_or_blocks, dict) else msg_or_blocks
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
    for a in _attachments(msg_or_blocks):
        for key in ("pretext", "text", "footer"):
            if a.get(key):
                parts.append(a[key])
    return "\n".join(parts)


def _slack_buttons(msg) -> List[Dict[str, Any]]:
    """Normalize Block Kit and legacy-attachment buttons to {text, url, style}."""
    out: List[Dict[str, Any]] = []
    for b in msg.get("blocks", []):
        if b.get("type") == "actions":
            for e in b["elements"]:
                out.append(
                    {"text": e.get("text", {}).get("text", ""), "url": e.get("url", ""), "style": e.get("style")}
                )
    for a in _attachments(msg):
        for e in a.get("actions", []) or []:
            out.append({"text": e.get("text", ""), "url": e.get("url", ""), "style": e.get("style")})
    return out


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
    def test_summary_stats_block_removed_from_slack_brief(self):
        # The In-this-brief / By-category / New-since-yesterday / resolved
        # stats block was cut from the Slack brief (user call, 2026-07-16):
        # headline + items carry the message. Teams/GChat keep their own.
        params = _params(
            new_counts=NewCounts(act_now=1, critical=3, high=2),
            resolved_count=2,
            resolved_savings=312.0,
            carryover_count=195,
            category_savings={"RightSizing": 1800.0, "InfraUpgrade": 2100.0},
        )
        msg = get_recommendation_nudge_digest_message_template(params)
        text = _slack_text(msg)

        assert "You can cut $3,840/mo in prod-aws" in text
        assert "In this brief" not in text
        assert "By category" not in text
        assert "New since yesterday" not in text
        assert "resolved" not in text
        assert "still open from earlier" not in text

    def test_old_payload_omits_delta_lines(self):
        # No new_counts/resolved/carryover provided -> graceful degrade
        params = _params()  # leaves new_counts=None and all delta ints at 0
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))

        assert "You can cut $3,840/mo in prod-aws" in text
        assert "New since yesterday" not in text
        assert "still open from earlier" not in text

    def test_savings_zero_keeps_plain_title_headline(self):
        params = _params(
            total_recoverable_savings=0,
            new_counts=NewCounts(act_now=0, critical=3, high=0),
            carryover_count=247,
        )
        msg = get_recommendation_nudge_digest_message_template(params)

        assert "You can cut" not in _slack_text(msg)  # no savings headline on a $0 digest
        assert msg["blocks"][0]["type"] == "header"

    def test_footer_url_has_digest_utm_and_date(self):
        params = _params(new_counts=NewCounts(act_now=1, critical=0, high=0))
        msg = get_recommendation_nudge_digest_message_template(params)
        url = [b for b in _slack_buttons(msg) if b["text"] == "View All Recommendations"][0]["url"]
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
        # trailing facts line carries severity + account only — no score, no band word
        assert "Score" not in text and "/100" not in text
        assert msg["attachments"][0]["text"].endswith("Medium priority · Acct: prod-aws")

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
        assert "Resolved: 2 (saved $312/mo)" in text
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
        assert "*2 resolved* (saved $312/mo)" in text
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
        details = [b for b in _slack_buttons(msg) if b["text"] == "Details"][0]
        assert details["url"] == "https://app/optimise?id=rec-1#recommendations"

    def test_absolute_cta_url_untouched(self):
        msg = get_recommendation_nudge_digest_message_template(_params())
        details = [b for b in _slack_buttons(msg) if b["text"] == "Details"][0]
        assert details["url"].startswith("https://app/optimise?id=rec-1")

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
        assert "1 priority recommendations need action in prod" in text


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

    def test_category_chips_removed_with_summary_block(self):
        params = _params(category_savings={"RightSizing": 1800.0, "Configuration": 760.0, "InfraUpgrade": 2100.0})
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))
        assert "By category" not in text


# ---------------------------- Rightsizing change summary ----------------------------


def _pod_rec(recommendation) -> DigestRecommendation:
    return DigestRecommendation(
        id="rec-rs",
        rule_name="pod_right_sizing",
        resource_name="prod/Deployment/payments-api",
        finops_score=85,
        finops_band="Act Now",
        estimated_savings=184.0,
        severity="High",
        cta_url="https://app/optimise?id=rec-rs#recommendations",
        recommendation=recommendation,
    )


class TestRightsizingChangeSummary:
    """Rightsizing items must state the change itself (current -> recommended);
    a bare rule name + savings reads as 'too big a message for too little info'."""

    def test_pod_rightsizing_renders_cpu_and_memory_change(self):
        rec = _pod_rec(
            {
                "payments-api": [
                    {"resource": "cpu", "allocated": {"request": 2.0}, "recommended": {"request": 0.5}},
                    {
                        "resource": "memory",
                        "allocated": {"request": 4 * 1024**3},
                        "recommended": {"request": 1024**3},
                    },
                ]
            }
        )
        params = _params(
            recommendations_by_account={"acc-1": AccountRecommendations(account_name="prod-aws", recommendations=[rec])}
        )
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))
        assert "CPU *2 cores → 500m*" in text
        assert "Memory *4Gi → 1Gi*" in text

    def test_replica_rightsizing_renders_replica_change(self):
        rec = _pod_rec({"recommendation": {"allocated_replica": 8, "recommended_replica": 4}})
        rec.rule_name = "replica_right_sizing"
        params = _params(
            recommendations_by_account={"acc-1": AccountRecommendations(account_name="prod-aws", recommendations=[rec])}
        )
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))
        assert "Replicas *8 → 4*" in text

    def test_replica_series_fallback(self):
        rec = _pod_rec(
            {"recommendation": {"allocated": [{"replicas": 6}, {"replicas": 8}], "recommended": [{"replicas": 4}]}}
        )
        rec.rule_name = "replica_right_sizing"
        params = _params(
            recommendations_by_account={"acc-1": AccountRecommendations(account_name="prod-aws", recommendations=[rec])}
        )
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))
        assert "Replicas *8 → 4*" in text

    def test_multi_container_summary_shows_first_plus_count(self):
        rec = _pod_rec(
            {
                "app": [{"resource": "cpu", "allocated": {"request": 1.0}, "recommended": {"request": 0.25}}],
                "sidecar": [{"resource": "cpu", "allocated": {"request": 0.5}, "recommended": {"request": 0.1}}],
            }
        )
        params = _params(
            recommendations_by_account={"acc-1": AccountRecommendations(account_name="prod-aws", recommendations=[rec])}
        )
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))
        assert "+1 more containers" in text

    def test_missing_or_malformed_recommendation_degrades_silently(self):
        for bad in (None, {}, {"payments-api": "not-a-list"}, {"payments-api": [{"resource": "cpu"}]}, [1, 2]):
            rec = _pod_rec(bad)
            params = _params(
                recommendations_by_account={
                    "acc-1": AccountRecommendations(account_name="prod-aws", recommendations=[rec])
                }
            )
            text = _slack_text(get_recommendation_nudge_digest_message_template(params))
            assert "Save *$184/mo*" in text  # item still renders
            assert "→" not in text  # just without a change line

    def test_unchanged_values_render_no_change_line(self):
        rec = _pod_rec({"app": [{"resource": "cpu", "allocated": {"request": 1.0}, "recommended": {"request": 1.0}}]})
        params = _params(
            recommendations_by_account={"acc-1": AccountRecommendations(account_name="prod-aws", recommendations=[rec])}
        )
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))
        assert "→" not in text


class TestRenderedChromeGuards:
    """Guards for Slack chrome that burned us live: score leakage and the
    "Added by {app}" byline from Block Kit blocks inside attachments."""

    def test_no_score_anywhere(self):
        params = _params(new_counts=NewCounts(act_now=1, critical=3, high=2), resolved_count=2)
        text = _slack_text(get_recommendation_nudge_digest_message_template(params))
        assert "Score" not in text and "/100" not in text

    def test_digest_attachments_are_legacy(self):
        msg = get_recommendation_nudge_digest_message_template(_params())
        assert msg["attachments"], "items must ride attachments (severity stripe)"
        for a in msg["attachments"]:
            assert "blocks" not in a
            assert "text" in a["mrkdwn_in"]

    def test_nudge_attachments_are_legacy(self):
        from notifications_server.message_templates.slack.recommendation_proactive_nudge import (
            ProactiveNudgeParams,
            get_recommendation_proactive_nudge_message_template,
        )

        params = ProactiveNudgeParams(
            total_recommendations=1,
            total_recoverable_savings=100.0,
            recommendations_by_account={
                "acc-1": AccountRecommendations(account_name="prod", recommendations=[_pod_rec({})])
            },
            base_url="https://app",
        )
        msg = get_recommendation_proactive_nudge_message_template(params)
        for a in msg["attachments"]:
            assert "blocks" not in a
        labels = [b["text"] for a in msg["attachments"] for b in a.get("actions", []) or []]
        assert "Ask Nubi" in labels and "View All Recommendations" in labels

    def test_item_facts_line_format(self):
        msg = get_recommendation_nudge_digest_message_template(_params())
        assert msg["attachments"][0]["text"].endswith("High priority · Acct: prod-aws")

    def test_no_footer_or_ts_keys_anywhere(self):
        # Slack appends "Added by {app}" to the attachment footer row, so the
        # footer/ts fields are banned alongside blocks-in-attachments.
        msg = get_recommendation_nudge_digest_message_template(_params())
        for a in msg["attachments"]:
            assert "footer" not in a and "ts" not in a and "blocks" not in a

    def test_headline_is_a_header_block(self):
        msg = get_recommendation_nudge_digest_message_template(_params())
        assert msg["blocks"][0]["type"] == "header"
        assert "You can cut $3,840/mo in prod-aws" == msg["blocks"][0]["text"]["text"]

    def test_resolution_is_outcome_first(self):
        from notifications_server.message_templates.slack.recommendation_resolution import (
            get_recommendation_resolution_message_params,
            get_recommendation_resolution_message_template,
        )

        params = get_recommendation_resolution_message_params(
            recommendation_id="rec-42",
            rule_name="pod_right_sizing",
            resource_name="payments/Deployment/checkout-api",
            account_id="acc-1",
            account_name="prod-aws",
            estimated_savings=820.0,
            severity="High",
            status="Closed",
            resolution={"resolver": "auto", "type": "applied", "status": "success"},
            base_url="https://app",
        )
        msg = get_recommendation_resolution_message_template(params)
        assert msg["blocks"][0]["type"] == "header"
        assert msg["blocks"][0]["text"]["text"] == "$820/mo recovered — Workload rightsizing"
        att = msg["attachments"][0]
        assert "footer" not in att and "blocks" not in att
        assert "Applied automatically" in att["text"]
        assert "Resolver:" not in att["text"] and "Status:" not in att["text"]  # no KV jargon
        labels = [b["text"] for b in att["actions"]]
        assert labels == ["View Details", "View All Recommendations"]

    def test_resolution_dismissed_never_claims_savings(self):
        from notifications_server.message_templates.slack.recommendation_resolution import (
            get_recommendation_resolution_message_params,
            get_recommendation_resolution_message_template,
        )

        params = get_recommendation_resolution_message_params(
            recommendation_id="rec-43",
            rule_name="pod_right_sizing",
            resource_name="x",
            estimated_savings=500.0,
            status="Dismissed",
            base_url="https://app",
        )
        msg = get_recommendation_resolution_message_template(params)
        assert msg["blocks"][0]["text"]["text"] == "Dismissed — Workload rightsizing"
        assert "recovered" not in msg["text"]
