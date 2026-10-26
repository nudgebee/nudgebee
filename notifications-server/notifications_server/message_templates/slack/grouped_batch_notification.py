"""Hourly digest for a rule set to "Group into one message per hour" —
generic findings, not anomaly/SLO-specific, so fields are read defensively
rather than required."""

from datetime import datetime, timedelta, timezone
from typing import Any, Dict, List, Optional

from pydantic import BaseModel, Field

from notifications_server.configs.settings import settings, URLRoutes
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_NEUTRAL,
    header_block,
    item_facts_line,
    legacy_attachment,
    link_button,
    neutral_footer_attachment,
)

MAX_BATCH_ITEMS = 5
# No per-item detection lag to account for here (unlike anomalies), so the
# footer's "View All" window is just the flush delay, floored at an hour.
FOOTER_WINDOW_FLOOR = timedelta(hours=1)


class BatchFindingItem(BaseModel):
    title: str = "Untitled finding"
    severity: str = ""
    account: str = ""
    account_id: str = ""
    finding_id: str = ""


class BatchFindingsSummaryParams(BaseModel):
    items: List[BatchFindingItem]
    overflow_count: int
    total_count: int
    account_ids: List[str] = Field(default_factory=list)
    window_start_ms: int = 0
    window_end_ms: int = 0


def _item_from(finding: Dict[str, Any], account_names: Dict[str, str]) -> BatchFindingItem:
    account_id = finding.get("cloud_account_id") or finding.get("account_id") or ""
    return BatchFindingItem(
        title=finding.get("title") or "Untitled finding",
        severity=(finding.get("priority") or "").title(),
        account=account_names.get(account_id) or account_id,
        account_id=account_id,
        finding_id=finding.get("id") or "",
    )


def get_batch_findings_aggregated_message_params(
    events: List[Dict[str, Any]], account_names: Optional[Dict[str, str]] = None
) -> BatchFindingsSummaryParams:
    """account_names maps account id to its name; a finding carries only the id."""
    items = [_item_from(event, account_names or {}) for event in events[:MAX_BATCH_ITEMS]]
    account_ids = sorted({a for a in (e.get("cloud_account_id") or e.get("account_id") for e in events) if a})
    # Computed once here, shared by every platform's "View All" link — only
    # the utm_source differs, so each renderer builds its own URL from these.
    now = datetime.now(timezone.utc)
    flush_delay = timedelta(seconds=settings.notifications.grouped_flush_delay_seconds)
    window_start = now - max(flush_delay, FOOTER_WINDOW_FLOOR)
    return BatchFindingsSummaryParams(
        items=items,
        overflow_count=max(len(events) - MAX_BATCH_ITEMS, 0),
        total_count=len(events),
        account_ids=account_ids,
        window_start_ms=int(window_start.timestamp() * 1000),
        window_end_ms=int(now.timestamp() * 1000),
    )


def view_all_url(input_data: BatchFindingsSummaryParams, utm_source: str) -> str:
    """The aggregate "View All Findings" link, shared by every platform once
    some findings overflowed past what's shown individually."""
    return settings.urls.troubleshoot_url(
        input_data.account_ids,
        start_time_ms=input_data.window_start_ms,
        end_time_ms=input_data.window_end_ms,
        utm_source=utm_source,
    )


def item_details_url(item: BatchFindingItem, utm_source: str) -> Optional[str]:
    """Per-item "Details" link, shared by every platform. None when the
    finding carries no id (events predating this field, or malformed)."""
    if not (item.finding_id and item.account_id):
        return None
    return settings.urls.investigate_url(item.account_id, item.finding_id, utm_source=utm_source)


def get_grouped_batch_findings_template(input_data: BatchFindingsSummaryParams) -> Dict[str, Any]:
    noun = "finding" if input_data.total_count == 1 else "findings"
    headline = f"{input_data.total_count} {noun} held for this hour's digest"

    blocks = [header_block(headline), {"type": "divider"}]
    attachments = []
    for item in input_data.items:
        facts = item_facts_line(item.severity, item.account)
        text = f"*{item.title}*" + (f"\n{facts}" if facts else "")
        details_url = item_details_url(item, URLRoutes.UTMSource.SLACK)
        actions = [link_button("Details", details_url, style="primary")] if details_url else None
        attachments.append(legacy_attachment(STRIPE_NEUTRAL, item.title, text=text, actions=actions))

    if input_data.overflow_count > 0:
        # Every shown item already has its own "Details" button; a "View All"
        # link only earns its place once some findings didn't fit.
        footer_actions = [
            link_button("View All Findings", view_all_url(input_data, URLRoutes.UTMSource.SLACK), style="primary")
        ]
        attachments.append(
            neutral_footer_attachment(
                text=f"_+{input_data.overflow_count} more findings_",
                actions=footer_actions,
                fallback="View all findings",
            )
        )

    return {
        "text": headline,
        "blocks": blocks,
        "attachments": attachments,
        "unfurl_links": False,
    }
