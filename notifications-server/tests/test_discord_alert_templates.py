"""Tests for the Discord alert templates (SLO + grouped anomaly).

These render the same data the Slack/Teams/GChat templates do, as Discord
embeds. They are pure functions, so we assert the {content, embeds} payload
shape and a few key fields without any network/dispatch.
"""

from notifications_server.message_templates.slack.slo import SLOAlertParams
from notifications_server.message_templates.slack.grouped_slo_notification import SLOAlertSummaryParams
from notifications_server.message_templates.slack.grouped_anomaly_notification import (
    AnomalyAlertParams,
    AnomalyAlertSummaryParams,
)
from notifications_server.message_templates.discord.slo import (
    get_discord_slo_alert_template,
    get_discord_grouped_slo_alert_template,
)
from notifications_server.message_templates.discord.grouped_anomaly import (
    get_discord_grouped_anomaly_template,
)


def _slo(**over):
    base = dict(
        account_id="acc-1",
        account_name="prod",
        namespace="payments",
        workload="api",
        status="Breached",
        slo_name="api-availability",
        slo_type="availability",
        slo_target="99.9",
        current_value="98.2",
        firing_since=1700000000,
        bad_event_count=42,
        good_event_count=1000,
        threshold="99.0",
        burn_rate="2.5",
        error_budget_remaining="12%",
    )
    base.update(over)
    return SLOAlertParams(**base)


def _no_empty_field_values(embeds):
    for e in embeds:
        for f in e.get("fields", []):
            assert f["value"] != "" and f["value"] is not None, f"empty value for field {f['name']}"


def test_discord_slo_alert_shape():
    payload = get_discord_slo_alert_template(_slo())
    assert "content" in payload and payload["embeds"]
    embed = payload["embeds"][0]
    assert embed["title"] == "SLO Alert: api-availability"
    assert "payments" in embed["description"] and "prod" in embed["description"]
    names = {f["name"] for f in embed["fields"]}
    assert {"Status", "Target", "Current Value", "Burn Rate", "Bad Events"} <= names
    assert "timestamp" in embed  # firing_since parsed
    _no_empty_field_values(payload["embeds"])


def test_discord_slo_alert_handles_missing_optionals():
    # burn_rate / error_budget_remaining are Optional; must not produce empty values.
    payload = get_discord_slo_alert_template(_slo(burn_rate=None, error_budget_remaining=None))
    _no_empty_field_values(payload["embeds"])
    vals = {f["name"]: f["value"] for f in payload["embeds"][0]["fields"]}
    assert vals["Burn Rate"] == "—"
    assert vals["Budget Remaining"] == "—"


def test_discord_grouped_slo_groups_by_account_and_caps_embeds():
    events = [_slo(account_name=f"acct-{i}", slo_name=f"slo-{i}") for i in range(15)]
    payload = get_discord_grouped_slo_alert_template(SLOAlertSummaryParams(events=events))
    assert "15 SLO alerts across 15 account(s)" in payload["content"]
    assert len(payload["embeds"]) <= 10  # Discord caps embeds per message


def test_discord_grouped_slo_truncates_per_account():
    events = [_slo(account_name="same", slo_name=f"slo-{i}") for i in range(12)]
    payload = get_discord_grouped_slo_alert_template(SLOAlertSummaryParams(events=events))
    assert len(payload["embeds"]) == 1
    assert "more in this account" in payload["embeds"][0]["description"]
    assert "url" in payload["embeds"][0]


def _anomaly(**over):
    base = dict(
        id="a1",
        title="CPU spike",
        source="prometheus",
        priority="High",
        status="Open",
        subject_name="api",
        subject_namespace="payments",
        starts_at="2023-11-14T22:13:20Z",
        finding_id="f1",
        cluster="prod-cluster",
        cloud_account_id="acc-1",
    )
    base.update(over)
    return AnomalyAlertParams(**base)


def test_discord_grouped_anomaly_shape():
    events = [_anomaly(), _anomaly(cloud_account_id="acc-2", cluster="staging")]
    payload = get_discord_grouped_anomaly_template(AnomalyAlertSummaryParams(events=events))
    assert "2 anomalies detected across 2 account(s)" in payload["content"]
    assert len(payload["embeds"]) == 2
    assert any("CPU spike" in e["description"] for e in payload["embeds"])
