"""Migration of the last old-style Slack templates (slo, cloud cost, k8s
recommendation, default) to the severity-stripe standard: one colored legacy
attachment, no Block Kit `blocks`/`footer`/`ts` inside attachments (the
"Added by {app}" byline anti-pattern), and a URL button where the old template
had one."""

import json

from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_CRITICAL,
    STRIPE_NEUTRAL,
    STRIPE_SAVINGS,
)
from notifications_server.message_templates.slack.slo import (
    get_slo_alert_message_params,
    get_slo_alert_message_template,
)
from notifications_server.message_templates.slack.cloud_cost_summary import (
    get_cloud_cost_summary_message_params,
    get_cloud_cost_message_template,
)
from notifications_server.message_templates.slack.recommendation import (
    get_recommendation_message_params,
    get_recommendation_message_template,
)
from notifications_server.message_templates.slack.default import (
    get_default_message_params,
    get_slack_default_message_template,
)

STRIPES = {STRIPE_CRITICAL, STRIPE_NEUTRAL, STRIPE_SAVINGS}


def _assert_no_byline_keys(out):
    """No attachment may carry Block Kit blocks / footer / ts."""
    for att in out.get("attachments", []):
        assert not any(k in att for k in ("blocks", "footer", "ts")), att
        assert att["color"] in STRIPES


class TestSloMigration:
    def _render(self, **overrides):
        params = {
            "account_id": "acc-1",
            "account_name": "prod-eu",
            "namespace": "payments",
            "workload": "checkout-api",
            "status": "FIRING",
            "slo_name": "checkout-availability",
            "slo_type": "availability",
            "slo_target": "99.9%",
            "current_value": "99.2%",
            "firing_since": "2026-07-14 09:02:00",
            "bad_event_count": 312,
            "good_event_count": 48200,
            "threshold": "0.1%",
            "burn_rate": "14.3",
            "error_budget_remaining": "8%",
        }
        params.update(overrides)
        return get_slo_alert_message_template(get_slo_alert_message_params(**params))

    def test_single_striped_card(self):
        out = self._render()
        _assert_no_byline_keys(out)
        (att,) = out["attachments"]
        assert att["color"] == STRIPE_CRITICAL
        assert att["title"] == "SLO breach: checkout-availability"
        assert "acc-1" in att["title_link"] and att["title_link"] == att["actions"][0]["url"]
        assert att["actions"][0]["text"] == "View SLO"

    def test_all_stats_are_kept(self):
        # Regression: the old card showed threshold + good/bad events; the
        # migration must not drop them.
        (att,) = self._render()["attachments"]
        rendered = {f["title"]: f["value"] for f in att["fields"]}
        assert rendered["Target"] == "99.9%"
        assert rendered["Current"] == "99.2%"
        assert rendered["Burn rate"] == "14.3"
        assert rendered["Budget remaining"] == "8%"
        assert rendered["Threshold"] == "0.1%"
        assert "312" in rendered["Events (bad/good)"] and "48200" in rendered["Events (bad/good)"]

    def test_no_hardcoded_fallback_date(self):
        (att,) = self._render()["attachments"]
        blob = json.dumps(att)
        assert "April" not in blob and "2024" not in blob
        assert "<!date^" in blob  # real localized token


class TestCloudCostMigration:
    def _render(self):
        return get_cloud_cost_message_template(
            get_cloud_cost_summary_message_params(
                account_id="acc-1",
                account_name="prod-aws",
                period={"start": "2026-07-12", "end": "2026-07-19"},
                title="Cloud Cost Report",
                total_daily_cost=1240.5,
                total_monthly_cost=37215.0,
                cost_currency="USD",
                summary={
                    "top_5_daily_items": [{"cost": 612, "product_code": "EC2"}],
                    "top_5_monthly_services": [{"cost": 18200, "service": "Compute"}],
                },
            )
        )

    def test_striped_card_with_fields_and_button(self):
        out = self._render()
        _assert_no_byline_keys(out)
        assert len(out["blocks"]) == 1 and out["blocks"][0]["type"] == "header"
        (att,) = out["attachments"]
        assert att["color"] == STRIPE_NEUTRAL
        titles = {f["title"] for f in att["fields"]}
        assert {"Account", "Period", "Daily cost", "Monthly cost"} <= titles
        assert att["actions"][0]["text"] == "View Cost Details"
        assert "/cloud-account/details/acc-1" in att["actions"][0]["url"]
        assert "EC2" in att["text"]


class TestRecommendationMigration:
    def _render(self):
        return get_recommendation_message_template(
            get_recommendation_message_params(
                account_id="acc-1",
                account_name="prod-eu",
                category="Rightsizing",
                total_new=12,
                total_archived=3,
                total_affected_resources=9,
                recommendations=[
                    {"summary": "Rightsize checkout-api", "savings": 84, "status": "NEW"},
                    {"summary": "Rightsize worker-pool", "savings": 61, "status": "NEW"},
                ],
            )
        )

    def test_posture_card_now_has_a_button(self):
        # Regression: the old template had NO button at all.
        out = self._render()
        _assert_no_byline_keys(out)
        assert out["attachments"][0]["color"] == STRIPE_SAVINGS
        footer = out["attachments"][-1]
        assert footer["actions"][0]["text"] == "View recommendations"
        assert "#optimize/summary" in footer["actions"][0]["url"]
        body = out["attachments"][0]["text"]
        assert "12 new" in body and "Rightsize checkout-api" in body
        assert "$84/mo" in body  # no float artifact


class TestDefaultMigration:
    def test_header_plus_neutral_attachment(self):
        out = get_slack_default_message_template(
            get_default_message_params(title="NudgeBee Notification", message="Cluster sync completed.")
        )
        _assert_no_byline_keys(out)
        assert out["blocks"][0]["type"] == "header"
        (att,) = out["attachments"]
        assert att["color"] == STRIPE_NEUTRAL
        assert "Cluster sync completed." in att["text"]
