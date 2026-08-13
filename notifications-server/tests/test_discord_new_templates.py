"""Tests for the Discord template renderers added for platform parity.

Covers the nine template slots that were previously None for Discord in
message_templates/__init__.py. Each renderer is a pure function returning the
{content, embeds} payload DiscordClient.chat_post consumes, with clamp_embeds
guaranteeing the payload stays inside Discord's hard limits.
"""

from datetime import datetime
from uuid import uuid4

from notifications_server.message_templates.discord.embed_utils import (
    MAX_EMBEDS,
    MAX_TOTAL_CHARS,
    _embed_char_count,
    clamp_embeds,
)
from notifications_server.message_templates.discord.auto_optimize_scheduled_notification import (
    get_discord_auto_optimize_schedule_message_template,
)
from notifications_server.message_templates.discord.cloud_cost_summary import (
    get_discord_cloud_cost_message_template,
)
from notifications_server.message_templates.discord.daily_highlight import get_discord_daily_highlight_template
from notifications_server.message_templates.discord.event_summary import get_discord_events_summary_template
from notifications_server.message_templates.discord.generic import get_discord_generic_message_template
from notifications_server.message_templates.discord.recommendation import get_discord_recommendation_template
from notifications_server.message_templates.discord.recommendation_nudge_digest import (
    get_discord_recommendation_nudge_digest_template,
)
from notifications_server.message_templates.discord.recommendation_proactive_nudge import (
    get_discord_recommendation_proactive_nudge_template,
)
from notifications_server.message_templates.discord.recommendation_resolution import (
    get_discord_recommendation_resolution_template,
)
from notifications_server.message_templates.slack.auto_optimize_scheduled_notification import (
    AutoOptimizeResourceFilter,
    AutopilotScheduledParams,
    TaskConfig,
)
from notifications_server.message_templates.slack.cloud_cost_summary import (
    CloudCostSummary,
    CostPeriod,
    CostSummary,
    DailyItem,
    MonthlyService,
)
from notifications_server.message_templates.slack.daily_highlight import (
    Account as RecapAccount,
    DailyRecapParams,
    EventCounts as RecapEventCounts,
    EventRows,
    Highlight,
    HighlightData,
    Insight,
    Insights,
    InsightsData,
    Recommendation as RecapRecommendation,
    RecommendationRow,
)
from notifications_server.message_templates.slack.events_summary import (
    Account as SummaryAccount,
    AccountsData,
    AccountsDataD,
    EventCountRow,
    EventCounts as SummaryEventCounts,
    EventCountsData,
    EventCountsDataD,
    EventsSummarised,
    EventsSummarisedData,
    EventsSummarisedDataD,
    EventsSummaryPayload,
    EventSummarisedRow,
)
from notifications_server.message_templates.slack.generic import GenericMessageParams, WorkflowMetadata
from notifications_server.message_templates.slack.recommendation import (
    RecommendationParams,
    RecommendationSummary,
)
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    AccountRecommendations,
    DigestRecommendation,
    NewCounts,
    RecommendationNudgeDigestParams,
)
from notifications_server.message_templates.slack.recommendation_proactive_nudge import ProactiveNudgeParams
from notifications_server.message_templates.slack.recommendation_resolution import (
    RecommendationResolutionParams,
    ResolutionDetails,
)


def _assert_discord_payload(payload):
    assert isinstance(payload["content"], str)
    assert payload["embeds"], "expected at least one embed"
    assert len(payload["embeds"]) <= MAX_EMBEDS
    assert sum(_embed_char_count(e) for e in payload["embeds"]) <= MAX_TOTAL_CHARS


# ---------------------------------------------------------------- clamp_embeds


def test_clamp_embeds_enforces_per_component_limits():
    embed = {
        "title": "t" * 300,
        "description": "d" * 200,
        "fields": [{"name": "n" * 300, "value": "v" * 50}],
        "footer": {"text": "f" * 3000},
    }
    out = clamp_embeds([embed])[0]
    assert len(out["title"]) == 256 and out["title"].endswith("…")
    assert len(out["fields"][0]["name"]) == 256
    assert len(out["footer"]["text"]) == 2048


def test_clamp_embeds_caps_field_count():
    embed = {"title": "t", "fields": [{"name": f"n{i}", "value": "v"} for i in range(30)]}
    out = clamp_embeds([embed])[0]
    assert len(out["fields"]) == 25


def test_clamp_embeds_drops_whole_trailing_embeds_first():
    embeds = [{"title": "e", "description": "d" * 3000} for _ in range(12)]
    out = clamp_embeds(embeds)
    assert len(out) <= MAX_EMBEDS
    assert sum(_embed_char_count(e) for e in out) <= MAX_TOTAL_CHARS
    # trimming dropped whole trailing embeds; the kept one is untruncated
    assert len(out) == 1
    assert out[0]["description"] == "d" * 3000


def test_clamp_embeds_truncates_last_description_with_ellipsis():
    # A single embed can exceed the total cap even when each component is
    # within its own limit (256 + 4000 + 2048 = 6304 > 5900).
    embed = {"title": "t" * 256, "description": "d" * 4000, "footer": {"text": "f" * 2048}}
    out = clamp_embeds([embed])
    assert len(out) == 1
    assert _embed_char_count(out[0]) <= MAX_TOTAL_CHARS
    assert out[0]["description"].endswith("…")


def test_clamp_embeds_does_not_mutate_input():
    embeds = [{"title": "t" * 300, "fields": [{"name": "n", "value": "v" * 2000}]}]
    clamp_embeds(embeds)
    assert len(embeds[0]["title"]) == 300
    assert len(embeds[0]["fields"][0]["value"]) == 2000


# --------------------------------------------------------------------- generic


def test_discord_generic_renders_message_text():
    payload = get_discord_generic_message_template(GenericMessageParams(message="Deployment restarted successfully"))
    _assert_discord_payload(payload)
    assert "Deployment restarted successfully" in payload["embeds"][0]["description"]


def test_discord_generic_appends_workflow_footer():
    params = GenericMessageParams(
        message="done",
        workflow_metadata=WorkflowMetadata(workflow_name="restart-pods", triggered_by="alice"),
    )
    payload = get_discord_generic_message_template(params)
    description = payload["embeds"][0]["description"]
    assert "Automation: restart-pods" in description
    assert "Triggered by: alice" in description


# ------------------------------------------------------------- daily highlight


def _daily_recap_params():
    highlight = Highlight(
        data=HighlightData(
            node_events=EventRows(rows=[RecapEventCounts(event_count=2, account_id="acc-1")]),
            pod_events=EventRows(rows=[RecapEventCounts(event_count=1, account_id="acc-1")]),
            app_events=EventRows(rows=[RecapEventCounts(event_count=3, account_id="acc-1")]),
            recommendation_groupings=RecommendationRow(
                rows=[
                    RecapRecommendation(
                        category="RightSizing",
                        rule_name="right_sizing",
                        count=4,
                        account_id="acc-1",
                        sum_estimated_savings=120.5,
                    )
                ]
            ),
        )
    )
    insights = Insights(
        data=InsightsData(
            cloud_accounts=[RecapAccount(id="acc-1", account_name="prod")],
            insight=[
                Insight(account_id="acc-1", title="OOMKill detected", type="Troubleshooting", unique_id="u1"),
            ],
        ),
        errors=None,
    )
    return DailyRecapParams(
        highlight1=highlight,
        highlight2=None,
        insights=insights,
        organization_id="org-1",
        organization_name="Acme",
        title="Daily Recap",
        total_opportunity_lost=10.0,
    )


def test_discord_daily_highlight_shape():
    payload = get_discord_daily_highlight_template(_daily_recap_params())
    _assert_discord_payload(payload)
    header = payload["embeds"][0]
    assert header["title"] == "Daily Recap - Acme"
    assert "$120.50/month" in header["description"]
    assert len(payload["embeds"]) == 2
    account_embed = payload["embeds"][1]
    assert account_embed["title"] == "Cluster: prod"
    assert "OOMKill detected" in account_embed["description"]
    assert "Application Errors: 3" in account_embed["description"]


# -------------------------------------------------------------- recommendation


def _recommendation_params(**over):
    base = dict(
        account_id="acc-1",
        account_name="prod",
        category="RightSizing",
        rule_name="right_sizing",
        rule_description="Requests exceed usage",
        total_estimated_savings=99.5,
        total_new=3,
        total_archived=1,
        total_affected_resources=5,
        recommendations=[RecommendationSummary(id="r1", summary="Reduce CPU for api", status="Open", savings=42.0)],
    )
    base.update(over)
    return RecommendationParams(**base)


def test_discord_recommendation_shape():
    payload = get_discord_recommendation_template(_recommendation_params())
    _assert_discord_payload(payload)
    header = payload["embeds"][0]
    assert header["title"] == "Kubernetes RightSizing Recommendations"
    assert "optimize/summary" in header["description"]  # same anchor as the Slack deep link
    assert "utm=discord" in header["description"]
    assert "3 new" in header["description"] and "1 resolved" in header["description"]
    assert "Reduce CPU for api" in payload["embeds"][1]["description"]


def test_discord_recommendation_security_grouped_by_severity():
    params = _recommendation_params(
        category="Security",
        recommendations=[
            RecommendationSummary(id="r1", summary="CVE in nginx", status="Open", severity="critical"),
            RecommendationSummary(id="r2", summary="CVE in redis", status="Open", severity="high"),
        ],
    )
    payload = get_discord_recommendation_template(params)
    assert "security/image-scan" in payload["embeds"][0]["description"]
    body = payload["embeds"][1]["description"]
    assert body.index("Critical Severity") < body.index("High Severity")


# ------------------------------------------------------ auto optimize schedule


def test_discord_auto_optimize_schedule_shape():
    params = AutopilotScheduledParams(
        status="scheduled",
        execution_on=datetime(2026, 7, 14, 10, 0, 0),
        auto_pilot_id=uuid4(),
        autopilot_name="nightly-rightsizing",
        tasks=[
            TaskConfig(
                action="patch",
                task_type="rightsizing",
                description="Apply CPU requests",
                resource_details=AutoOptimizeResourceFilter(name="api", namespace="payments", type="Deployment"),
            )
        ],
        tenant_id=uuid4(),
        cluster="prod",
        auto_pilot_link="https://example.com/auto-pilot",
    )
    payload = get_discord_auto_optimize_schedule_message_template(params)
    _assert_discord_payload(payload)
    embed = payload["embeds"][0]
    assert "nightly-rightsizing" in embed["title"]
    assert "/auto-pilot/task/" in embed["description"]
    field = embed["fields"][0]
    assert "rightsizing" in field["name"]
    assert "api" in field["value"] and "payments" in field["value"]


# -------------------------------------------------------------- events summary


def _events_summary_payload():
    return EventsSummaryPayload(
        title="Events Summary",
        accounts=AccountsDataD(data=AccountsData(accounts=[SummaryAccount(id="acc-1", account_name="prod")])),
        event_counts=EventCountsDataD(
            data=EventCountsData(
                event_groupings=SummaryEventCounts(
                    rows=[
                        EventCountRow(
                            account_id="acc-1",
                            event_count=12,
                            count_priority_high=4,
                            count_priority_medium=5,
                            count_priority_low=3,
                            count_priority_debug=0,
                            count_priority_info=0,
                            count_application_issues=6,
                            count_node_issues=2,
                            count_pod_issues=4,
                        )
                    ]
                )
            )
        ),
        events_summarised=EventsSummarisedDataD(
            data=EventsSummarisedData(
                event_groupings=EventsSummarised(
                    rows=[
                        EventSummarisedRow(
                            account_id="acc-1",
                            max_created_at=datetime(2026, 7, 13, 9, 0, 0),
                            event_count=7,
                            aggregation_key="CrashLoopBackOff",
                        )
                    ]
                )
            )
        ),
        organization_id="org-1",
        organization_name="Acme",
    )


def test_discord_events_summary_shape():
    payload = get_discord_events_summary_template(_events_summary_payload())
    _assert_discord_payload(payload)
    embed = payload["embeds"][0]
    assert embed["title"] == "Account: prod"
    assert "**Total Events:** 12" in embed["description"]
    assert "CrashLoopBackOff" in embed["description"]
    assert "Acme" in payload["content"]


# ---------------------------------------------------------- cloud cost summary


def test_discord_cloud_cost_shape():
    params = CloudCostSummary(
        account_id="acc-1",
        account_name="prod-aws",
        period=CostPeriod(start="2026-07-01", end="2026-07-13"),
        title="Cloud Cost Report",
        total_daily_cost=123.45,
        total_monthly_cost=3456.78,
        summary=CostSummary(
            top_5_daily_items=[DailyItem(cost=50.5, product_code="AmazonEC2")],
            top_5_monthly_services=[MonthlyService(cost=900.0, service="AmazonRDS")],
        ),
    )
    payload = get_discord_cloud_cost_message_template(params)
    _assert_discord_payload(payload)
    description = payload["embeds"][0]["description"]
    assert "$123.45" in description and "$3,456.78" in description
    assert "AmazonEC2" in description and "AmazonRDS" in description
    assert "/cloud-account/details/acc-1?tab=0&utm=discord" in description


# ---------------------------------------------------------------- nudge digest


def _digest_rec(**over):
    base = dict(
        id="r1",
        rule_name="right_sizing",
        resource_name="api",
        finops_score=88,
        finops_band="Act Now",
        estimated_savings=42.0,
        severity="High",
        category="RightSizing",
        cta_url="https://example.com/review",
    )
    base.update(over)
    return DigestRecommendation(**base)


def test_discord_nudge_digest_shape():
    params = RecommendationNudgeDigestParams(
        organization_id="org-1",
        organization_name="Acme",
        total_recoverable_savings=42.0,
        act_now_count=1,
        recommendations_by_account={
            "acc-1": AccountRecommendations(account_name="prod", recommendations=[_digest_rec()])
        },
        new_counts=NewCounts(act_now=1),
        digest_date="2026-07-13",
    )
    payload = get_discord_recommendation_nudge_digest_template(params)
    _assert_discord_payload(payload)
    header = payload["embeds"][0]
    assert "FinOps Daily Brief" in header["title"]
    assert "recoverable" in header["description"]
    band_embed = payload["embeds"][1]
    assert band_embed["title"] == "Priority"  # "Act Now" display name
    assert "**api**" in band_embed["description"]
    assert "utm=discord-digest" in payload["embeds"][-1]["description"]
    assert "d=2026-07-13" in payload["embeds"][-1]["description"]


# ------------------------------------------------------------- proactive nudge


def test_discord_proactive_nudge_shape():
    params = ProactiveNudgeParams(
        organization_id="org-1",
        total_recommendations=1,
        total_recoverable_savings=42.0,
        recommendations_by_account={
            "acc-1": AccountRecommendations(account_name="prod", recommendations=[_digest_rec()])
        },
    )
    payload = get_discord_recommendation_proactive_nudge_template(params)
    _assert_discord_payload(payload)
    assert "FinOps Alert" in payload["embeds"][0]["title"]
    account_embed = payload["embeds"][1]
    assert account_embed["title"] == "prod"
    assert "Score" not in account_embed["description"]  # scores removed from all recommendation surfaces
    assert "Savings: $42/mo" in account_embed["description"]
    # single-account bundle deep-links Nubi scoped to that account
    assert "/ask-nudgebee" in account_embed["description"]
    assert "accountId=acc-1" in account_embed["description"]


# ------------------------------------------------------------------ resolution


def test_discord_recommendation_resolution_shape():
    params = RecommendationResolutionParams(
        organization_id="org-1",
        recommendation_id="rec-9",
        resource_name="api",
        rule_name="right_sizing",
        category="RightSizing",
        account_id="acc-1",
        account_name="prod",
        finops_score=90,
        finops_band="Act Now",
        estimated_savings=42.0,
        severity="High",
        status="Resolved",
        resolution=ResolutionDetails(
            resolver="auto", type="pr", type_reference="PR-1", status="merged", status_message="done"
        ),
    )
    payload = get_discord_recommendation_resolution_template(params)
    _assert_discord_payload(payload)
    embed = payload["embeds"][0]
    assert "Recommendation Resolved" in embed["title"]
    assert "id=rec-9#resolutions" in embed["description"]
    fields = {f["name"]: f["value"] for f in embed["fields"]}
    assert fields["Status"] == "Resolved"
    assert fields["Resolver"] == "auto"
    assert fields["Message"] == "done"
