from datetime import datetime
from typing import List, Dict, Any
from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.base import Emojis


class Account(BaseModel):
    id: str
    account_name: str


class AccountsData(BaseModel):
    accounts: List[Account]


class AccountsDataD(BaseModel):
    data: AccountsData


class EventCountRow(BaseModel):
    account_id: str
    event_count: int
    count_priority_high: int
    count_priority_medium: int
    count_priority_low: int
    count_priority_debug: int
    count_priority_info: int
    count_application_issues: int
    count_node_issues: int
    count_pod_issues: int


class EventCounts(BaseModel):
    rows: List[EventCountRow]


class EventCountsData(BaseModel):
    event_groupings: EventCounts


class EventCountsDataD(BaseModel):
    data: EventCountsData


class EventSummarisedRow(BaseModel):
    account_id: str
    max_created_at: datetime
    event_count: int
    aggregation_key: str


class EventsSummarised(BaseModel):
    rows: List[EventSummarisedRow]


class EventsSummarisedData(BaseModel):
    event_groupings: EventsSummarised


class EventsSummarisedDataD(BaseModel):
    data: EventsSummarisedData


class EventsSummaryPayload(BaseModel):
    title: str
    accounts: AccountsDataD
    event_counts: EventCountsDataD
    events_summarised: EventsSummarisedDataD
    organization_id: str
    organization_name: str


def create_text_for_account(
    account_name: str, account_id: str, counts: EventCountRow, events_summarised: List[EventSummarisedRow]
) -> List[str]:
    """
    Build lines of plain text for a single account's event summary,
    including severity breakdown and top events, with bold labels.
    """
    lines: List[str] = []
    # Account header with link
    lines.append(f"**Account:** {account_name} (<{public_ip()}/kubernetes/details/{account_id}>)")
    # Total Events
    lines.append(f"**Total Events:** {counts.event_count}")
    # By severity
    lines.append(
        f"**High:** {counts.count_priority_high}, **Medium:** {counts.count_priority_medium}, **Low:**"
        f" {counts.count_priority_low}"
    )
    # By type
    lines.append(
        f"**Types** - **App Issues:** {counts.count_application_issues}, **Node Issues:** {counts.count_node_issues},"
        f" **Pod Issues:** {counts.count_pod_issues}"
    )
    # Summarised events: dedupe
    dedup: Dict[str, int] = {}
    for e in events_summarised:
        if e.account_id != account_id or e.event_count <= 0:
            continue
        dedup[e.aggregation_key] = dedup.get(e.aggregation_key, 0) + e.event_count
    if dedup:
        lines.append("**Top Events:**")
        for key, cnt in sorted(dedup.items(), key=lambda x: x[1], reverse=True):
            lines.append(f"- **{key}:** {cnt}")
    return lines


def get_teams_events_summary_template(payload: EventsSummaryPayload) -> Dict[str, Any]:
    body: List[Dict[str, Any]] = [
        {
            "type": "TextBlock",
            "text": (
                f"{Emojis.Source.value} **{settings.urls.branding_name} Events Summary** - {payload.organization_name}"
            ),
            "size": "Medium",
            "weight": "Bolder",
            "wrap": True,
        },
        {"type": "TextBlock", "text": "", "separator": True},
    ]

    event_counts = payload.event_counts.data.event_groupings.rows
    events_summarised = payload.events_summarised.data.event_groupings.rows
    for acct in payload.accounts.data.accounts:
        counts = next((r for r in event_counts if r.account_id == acct.id), None)
        if not counts or counts.event_count <= 0:
            continue
        lines = create_text_for_account(acct.account_name, acct.id, counts, events_summarised)
        for line in lines:
            body.append(
                {
                    "type": "TextBlock",
                    "text": line,
                    "wrap": True,
                    "spacing": "None",
                }
            )
        body.append({"type": "TextBlock", "text": "", "separator": True})

    card_content: Dict[str, Any] = {
        "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.2",
        "msteams": {"width": "Full"},
        "body": body,
    }

    return card_content
