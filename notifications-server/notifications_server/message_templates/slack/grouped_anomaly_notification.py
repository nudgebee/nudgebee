from typing import List, Dict, Any, Optional
from datetime import datetime
from pydantic import BaseModel

from notifications_server.configs.settings import settings, URLRoutes
from notifications_server.message_templates.slack.recommendation_nudge_digest import accounts_phrase

MAX_ANOMALY_ITEMS = 5


class AnomalyAlertParams(BaseModel):
    id: str
    title: str
    source: str
    priority: str
    status: str
    subject_name: str
    subject_namespace: str
    starts_at: str
    finding_id: str
    cluster: str
    cloud_account_id: str
    updated_at: Optional[str] = None
    subject_type: Optional[str] = None
    subject_owner: Optional[str] = None
    # Spend anomalies carry a baseline-vs-observed sentence here (e.g. "Daily
    # spend of $208 ... exceeds baseline average of $14"); metric anomalies set
    # it equal to the title and are filtered out at render.
    description: Optional[str] = None


def _anomaly_value_line(alert: AnomalyAlertParams) -> str:
    """The evidence line under the title. Only spend anomalies carry a
    baseline-vs-observed description distinct from the title; the trailing
    z-score is producer jargon, dropped for the channel."""
    desc = (alert.description or "").strip()
    if not desc or desc == (alert.title or "").strip():
        return ""
    return desc.split(" (z-score:")[0].strip()


class AnomalyAlertSummaryParams(BaseModel):
    events: List[AnomalyAlertParams]


def get_anomaly_aggregated_message_params(events: List[Dict[str, Any]]) -> AnomalyAlertSummaryParams:
    return AnomalyAlertSummaryParams(events=[AnomalyAlertParams(**e) for e in events])


def _started_block(starts_at: str) -> str:
    try:
        ts = int(datetime.fromisoformat(starts_at.replace("Z", "+00:00")).timestamp())
        fallback = datetime.fromtimestamp(ts).strftime("%d %b %Y %I:%M %p")
    except (TypeError, ValueError, OSError, OverflowError):
        return starts_at
    return f"<!date^{ts}^{{date_short_pretty}} {{time}}|{fallback}>"


def get_grouped_anomaly_alerts_template(input_data: List[AnomalyAlertParams]) -> Dict[str, Any]:
    if isinstance(input_data, AnomalyAlertSummaryParams):
        alerts: List[AnomalyAlertParams] = input_data.events
    else:
        alerts = input_data

    total_alerts = len(alerts)
    account_count = len({alert.cloud_account_id for alert in alerts})
    noun = "anomaly" if total_alerts == 1 else "anomalies"
    headline = f"{total_alerts} {noun} detected across {accounts_phrase(account_count)}"

    blocks: List[Dict[str, Any]] = [
        {"type": "section", "text": {"type": "mrkdwn", "text": f"*{headline}*"}},
        {"type": "divider"},
    ]

    for alert in alerts[:MAX_ANOMALY_ITEMS]:
        title = alert.title or f"{alert.subject_name} anomaly"
        blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": f"*{title}*"}})

        value_line = _anomaly_value_line(alert)
        if value_line:
            blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": value_line}})

        identity_bits = [
            f"{alert.priority.title()} priority" if alert.priority else "",
            f"started {_started_block(alert.starts_at)}",
            f"{alert.subject_name} ({alert.subject_namespace})" if alert.subject_namespace else alert.subject_name,
            f"Acct {alert.cluster or alert.cloud_account_id}",
        ]
        identity = " · ".join(bit for bit in identity_bits if bit)
        blocks.append({"type": "context", "elements": [{"type": "mrkdwn", "text": identity}]})

        if alert.finding_id:
            blocks.append(
                {
                    "type": "actions",
                    "elements": [
                        {
                            "type": "button",
                            "text": {"type": "plain_text", "text": "Details"},
                            "url": settings.urls.investigate_url(
                                alert.cloud_account_id, alert.finding_id, utm_source=URLRoutes.UTMSource.SLACK
                            ),
                        }
                    ],
                }
            )

    remaining = total_alerts - MAX_ANOMALY_ITEMS
    if remaining > 0:
        blocks.append(
            {"type": "section", "text": {"type": "mrkdwn", "text": f"_+{remaining} more anomalies detected_"}}
        )

    blocks.append(
        {
            "type": "context",
            "elements": [
                {
                    "type": "mrkdwn",
                    "text": "Review detected anomalies and investigate impacted workloads in your clusters.",
                }
            ],
        }
    )

    return {
        "text": headline,
        "blocks": blocks[:50],
        "unfurl_links": False,
    }
