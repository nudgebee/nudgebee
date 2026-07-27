import re
from typing import List, Dict, Any, Optional
from datetime import datetime, timezone
from pydantic import BaseModel

from notifications_server.configs.settings import settings, URLRoutes
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_HIGH,
    accounts_scope,
    header_block,
    legacy_attachment,
    link_button,
    neutral_footer_attachment,
)

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
    # Stat labels stamped by the producer (api-server services/anomoly).
    # Evidences never reach a notification — the post-process consumer nils them
    # before publishing — so the numbers an alert shows travel on labels.
    labels: Optional[Dict[str, Any]] = None


# Dollar amounts, percentages, and Nx multipliers in the producer sentence get
# mrkdwn bold so the observed-vs-baseline numbers pop (matches the mocks).
_STAT_PATTERN = re.compile(r"\$[\d,]+(?:\.\d+)?|\b\d+(?:\.\d+)?(?:%|x|×)")


def _anomaly_value_line(alert: AnomalyAlertParams) -> str:
    """The evidence line under the title. Only spend anomalies carry a
    baseline-vs-observed description distinct from the title; the trailing
    z-score is producer jargon, dropped for the channel."""
    desc = (alert.description or "").strip()
    if not desc or desc == (alert.title or "").strip():
        return ""
    value_line = desc.split(" (z-score:")[0].strip()
    return _STAT_PATTERN.sub(lambda m: f"*{m.group(0)}*", value_line)


LABEL_CURRENT = "anomaly_current"
LABEL_BASELINE = "anomaly_baseline"
LABEL_ZSCORE = "anomaly_zscore"
LABEL_CHANGE = "anomaly_change"


def _label(alert: AnomalyAlertParams, key: str) -> str:
    labels = alert.labels if isinstance(alert.labels, dict) else {}
    value = labels.get(key)
    return str(value).strip() if value not in (None, "") else ""


def _anomaly_stats_line(alert: AnomalyAlertParams) -> str:
    """The numbers under the title, read from the producer's stat labels.
    Spend anomalies lead with z-score and the change (their observed-vs-baseline
    already rides the description); metric anomalies — whose description equals
    the title, so they have no value line at all — get current vs baseline."""
    zscore = _label(alert, LABEL_ZSCORE)
    change = _label(alert, LABEL_CHANGE)
    if zscore or change:
        parts = []
        if zscore:
            parts.append(f"Z-score *{zscore}*")
        if change:
            parts.append(f"*{change}*")
        baseline = _label(alert, LABEL_BASELINE)
        if baseline:
            parts.append(f"expected *{baseline}*")
        return " · ".join(parts)

    current = _label(alert, LABEL_CURRENT)
    if not current:
        return ""
    baseline = _label(alert, LABEL_BASELINE)
    if baseline:
        return f"Current *{current}* vs baseline *{baseline}*"
    return f"Current value *{current}*"


class AnomalyAlertSummaryParams(BaseModel):
    events: List[AnomalyAlertParams]


def get_anomaly_aggregated_message_params(events: List[Dict[str, Any]]) -> AnomalyAlertSummaryParams:
    return AnomalyAlertSummaryParams(events=[AnomalyAlertParams(**e) for e in events])


def _started(starts_at: str) -> str:
    """Inline localized start time via Slack's <!date> token; raw producer
    string when it doesn't parse."""
    try:
        ts = int(datetime.fromisoformat(starts_at.replace("Z", "+00:00")).timestamp())
        fallback = datetime.fromtimestamp(ts, tz=timezone.utc).strftime("%d %b %Y %I:%M %p UTC")
    except (TypeError, ValueError, OSError, OverflowError):
        return f"started {starts_at}" if starts_at else ""
    return f"started <!date^{ts}^{{date_short_pretty}} {{time}}|{fallback}>"


def get_grouped_anomaly_alerts_template(input_data: List[AnomalyAlertParams]) -> Dict[str, Any]:
    if isinstance(input_data, AnomalyAlertSummaryParams):
        alerts: List[AnomalyAlertParams] = input_data.events
    else:
        alerts = input_data

    total_alerts = len(alerts)
    account_names = {alert.cloud_account_id: (alert.cluster or alert.cloud_account_id) for alert in alerts}
    noun = "anomaly" if total_alerts == 1 else "anomalies"
    headline = f"{total_alerts} {noun} detected {accounts_scope(list(account_names.values()))}"

    blocks: List[Dict[str, Any]] = [header_block(headline), {"type": "divider"}]

    attachments = []
    for alert in alerts[:MAX_ANOMALY_ITEMS]:
        title = alert.title or f"{alert.subject_name} anomaly"
        lines = [f"*{title}*"]
        value_line = _anomaly_value_line(alert)
        if value_line:
            lines.append(value_line)
        stats_line = _anomaly_stats_line(alert)
        if stats_line:
            lines.append(stats_line)

        subject = f"{alert.subject_name} ({alert.subject_namespace})" if alert.subject_namespace else alert.subject_name
        facts_bits = [
            f"{alert.priority.title()} priority" if alert.priority else "",
            subject,
            f"Acct: {alert.cluster or alert.cloud_account_id}",
            _started(alert.starts_at),
        ]
        lines.append(" · ".join(bit for bit in facts_bits if bit))

        actions = None
        if alert.id:
            details_url = settings.urls.investigate_url(
                alert.cloud_account_id, alert.id, utm_source=URLRoutes.UTMSource.SLACK
            )
            actions = [link_button("Details", details_url, style="primary")]

        attachments.append(legacy_attachment(STRIPE_HIGH, title, text="\n".join(lines), actions=actions))

    remaining = total_alerts - MAX_ANOMALY_ITEMS
    if remaining > 0:
        attachments.append(
            neutral_footer_attachment(text=f"_+{remaining} more anomalies detected_", fallback="Anomaly summary")
        )

    return {
        "text": headline,
        "blocks": blocks[:50],
        "attachments": attachments[:20],
        "unfurl_links": False,
    }
