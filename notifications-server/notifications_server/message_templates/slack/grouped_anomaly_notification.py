import re
from typing import List, Dict, Any, Optional, Tuple
from datetime import datetime, timedelta, timezone
from pydantic import BaseModel

from notifications_server.configs.settings import settings, URLRoutes
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_HIGH,
    STRIPE_SAVINGS,
    accounts_scope,
    header_block,
    legacy_attachment,
    link_button,
    neutral_footer_attachment,
)

MAX_ANOMALY_ITEMS = 5
# The Resolved section's own cap, shared by every platform's template. With the
# two clips below a full section stays near 1,400 characters, which is what the
# single-text platforms hold back for it before truncating the firing part.
MAX_RESOLVED_ITEMS = 5
RESOLVED_TITLE_MAX = 120
RESOLVED_WHERE_MAX = 100
RESOLVED_STATUSES = ("resolved", "closed")

# Matches event.source, which every anomaly producer stamps "anomaly"
# regardless of aggregation_key (api-server/services/anomoly).
ANOMALY_SOURCE = "anomaly"

# Footer link window: sized off grouped_flush_delay_seconds (every item in a
# batch was enqueued at most that long before flush) rather than a guess, with
# a floor/buffer for low-delay dev configs and clock skew.
FOOTER_WINDOW_FLOOR = timedelta(hours=1)
FOOTER_WINDOW_BUFFER = timedelta(minutes=15)


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
    # Set once the anomaly clears; the Resolved section's "resolved <time>".
    ends_at: Optional[str] = None
    # The Events page filters on created_at, so the "View All" window needs it
    # for a resolved anomaly first seen long before its last starts_at.
    created_at: Optional[str] = None
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
    # Set by the router on an anomaly that had cleared and fired again
    # (`finding_returned`); its line carries the "returned" label.
    returned: Optional[bool] = None


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


def _parse_starts_at(starts_at: Optional[str]) -> Optional[datetime]:
    """Producer starts_at (or ends_at, created_at) as a tz-aware datetime, or
    None if absent/unparseable."""
    if not starts_at:
        return None
    try:
        dt = datetime.fromisoformat(starts_at.replace("Z", "+00:00"))
    except (TypeError, ValueError, OSError, OverflowError):
        return None
    return dt if dt.tzinfo else dt.replace(tzinfo=timezone.utc)


def _utc_text(dt: datetime) -> str:
    return datetime.fromtimestamp(int(dt.timestamp()), tz=timezone.utc).strftime("%d %b %Y %I:%M %p UTC")


def _date_token(dt: datetime) -> str:
    """Slack's <!date> token: the viewer's local time, UTC text as fallback."""
    return f"<!date^{int(dt.timestamp())}^{{date_short_pretty}} {{time}}|{_utc_text(dt)}>"


def _started(starts_at: str) -> str:
    """Inline localized start time via Slack's <!date> token; raw producer
    string when it doesn't parse."""
    dt = _parse_starts_at(starts_at)
    if dt is None:
        return f"started {starts_at}" if starts_at else ""
    return f"started {_date_token(dt)}"


def split_resolved(alerts: List[AnomalyAlertParams]) -> Tuple[List[AnomalyAlertParams], List[AnomalyAlertParams]]:
    """(firing, resolved), each in batch order. With the flag off nothing is
    split out, so every template renders exactly as it did before the section."""
    if not settings.notifications.digest_resolved_enabled:
        return alerts, []
    firing: List[AnomalyAlertParams] = []
    resolved: List[AnomalyAlertParams] = []
    for alert in alerts:
        (resolved if (alert.status or "").strip().lower() in RESOLVED_STATUSES else firing).append(alert)
    return firing, resolved


def counts_phrase(firing: int, resolved: int, noun: str) -> str:
    """Headline counts: "3 anomalies detected, 2 resolved"; a digest of resolves
    only reads "2 anomalies resolved"."""
    if not resolved:
        return f"{firing} {noun} detected"
    if not firing:
        return f"{resolved} {noun} resolved"
    return f"{firing} {noun} detected, {resolved} resolved"


def returned_label(alert: AnomalyAlertParams, bold: str) -> str:
    """What follows the title of a firing anomaly that had cleared and fired
    again; empty for any other, whose line then renders as it always did.
    `bold` is the platform's marker."""
    return f" · {bold}returned{bold}" if alert.returned else ""


def _clip(text: str, limit: int) -> str:
    return text if len(text) <= limit else text[: limit - 1] + "…"


def resolved_line(alert: AnomalyAlertParams, bold: str, when: Optional[str] = None) -> str:
    """One compact line of the Resolved section: title, where it was
    (namespace/workload, then the account — spend anomalies have no namespace)
    and when it cleared, if the payload says. `bold` is the platform's marker;
    `when` overrides the plain UTC time (Slack passes its <!date> token)."""
    title = _clip(alert.title or f"{alert.subject_name} anomaly", RESOLVED_TITLE_MAX)
    subject = f"{alert.subject_namespace}/{alert.subject_name}" if alert.subject_namespace else alert.subject_name
    account = alert.cluster or alert.cloud_account_id
    places = [subject] if subject == account else [subject, account]
    where = _clip(" · ".join(place for place in places if place), RESOLVED_WHERE_MAX)
    if when is None:
        ended = _parse_starts_at(alert.ends_at)
        when = f"resolved {_utc_text(ended)}" if ended else ""
    return " · ".join(bit for bit in (f"{bold}{title}{bold}", where, when) if bit)


def get_grouped_anomaly_alerts_template(input_data: List[AnomalyAlertParams]) -> Dict[str, Any]:
    if isinstance(input_data, AnomalyAlertSummaryParams):
        alerts: List[AnomalyAlertParams] = input_data.events
    else:
        alerts = input_data

    firing, resolved = split_resolved(alerts)
    account_names = {alert.cloud_account_id: (alert.cluster or alert.cloud_account_id) for alert in alerts}
    noun = "anomaly" if (len(firing) or len(resolved)) == 1 else "anomalies"
    headline = f"{counts_phrase(len(firing), len(resolved), noun)} {accounts_scope(list(account_names.values()))}"

    blocks: List[Dict[str, Any]] = [header_block(headline), {"type": "divider"}]

    attachments = []
    for alert in firing[:MAX_ANOMALY_ITEMS]:
        title = alert.title or f"{alert.subject_name} anomaly"
        lines = [f"*{title}*{returned_label(alert, '*')}"]
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

    remaining = len(firing) - MAX_ANOMALY_ITEMS
    resolved_remaining = len(resolved) - MAX_RESOLVED_ITEMS
    footer_actions = None
    if remaining > 0 or resolved_remaining > 0:
        # Every shown item already has its own "Details" button; a "View All"
        # link only earns its place once some anomalies didn't fit.
        unique_account_ids = sorted({alert.cloud_account_id for alert in alerts if alert.cloud_account_id})
        now = datetime.now(timezone.utc)
        flush_delay = timedelta(seconds=settings.notifications.grouped_flush_delay_seconds)
        window_start = now - max(flush_delay, FOOTER_WINDOW_FLOOR) - FOOTER_WINDOW_BUFFER
        # Some anomalies (e.g. spend, or delayed detections) can carry a starts_at
        # well before the flush window; widen window_start so "View All" doesn't
        # filter them out of the Troubleshoot view.
        earliest_starts_at = [dt for dt in (_parse_starts_at(alert.starts_at) for alert in alerts) if dt]
        # The linked page filters on created_at, which for a resolved anomaly can
        # sit days before a starts_at that moved with every detection cycle.
        earliest_starts_at += [dt for dt in (_parse_starts_at(alert.created_at) for alert in resolved) if dt]
        # An unset timestamp arrives as year 1. It says nothing about when the
        # anomaly started, and it cannot be shifted back by the buffer.
        earliest_starts_at = [dt for dt in earliest_starts_at if dt.year > 1]
        if earliest_starts_at:
            window_start = min(window_start, min(earliest_starts_at) - FOOTER_WINDOW_BUFFER)
        footer_actions = [
            link_button(
                "View All Anomalies",
                settings.urls.troubleshoot_url(
                    unique_account_ids,
                    sources=[ANOMALY_SOURCE],
                    start_time_ms=int(window_start.timestamp() * 1000),
                    end_time_ms=int(now.timestamp() * 1000),
                    utm_source=URLRoutes.UTMSource.SLACK,
                ),
                style="primary",
            )
        ]
    if remaining > 0:
        attachments.append(
            neutral_footer_attachment(
                text=f"_+{remaining} more anomalies detected_", actions=footer_actions, fallback="View all anomalies"
            )
        )

    if resolved:
        resolved_lines = ["*Resolved*"]
        for alert in resolved[:MAX_RESOLVED_ITEMS]:
            ended = _parse_starts_at(alert.ends_at)
            when = f"resolved {_date_token(ended)}" if ended else ""
            resolved_lines.append(f"• {resolved_line(alert, '*', when)}")
        if resolved_remaining > 0:
            resolved_lines.append(f"_+{resolved_remaining} more resolved_")
        # One "View All" per message: the firing footer carries it when there is one.
        attachments.append(
            legacy_attachment(
                STRIPE_SAVINGS,
                "Resolved anomalies",
                text="\n".join(resolved_lines),
                actions=None if remaining > 0 else footer_actions,
            )
        )

    return {
        "text": headline,
        "blocks": blocks[:50],
        "attachments": attachments[:20],
        "unfurl_links": False,
    }
