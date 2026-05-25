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
    lines: List[str] = []
    # Account header with link
    lines.append(f"*Account:* {account_name} ({public_ip()}/kubernetes/details/{account_id})")
    # Total events
    lines.append(f"*Total Events:* {counts.event_count}")
    # By severity
    lines.append(
        f"*High:* {counts.count_priority_high}, *Medium:* {counts.count_priority_medium}, *Low:*"
        f" {counts.count_priority_low}"
    )
    # By type
    lines.append(
        f"*Types* - *App Issues:* {counts.count_application_issues}, *Node Issues:* {counts.count_node_issues}, *Pod"
        f" Issues:* {counts.count_pod_issues}"
    )
    # Summarised events: dedupe and limit to top 5
    dedup: Dict[str, int] = {}
    for e in events_summarised:
        if e.account_id != account_id or e.event_count <= 0:
            continue
        dedup[e.aggregation_key] = dedup.get(e.aggregation_key, 0) + e.event_count
    if dedup:
        lines.append("*Top Events:*")
        # Sort by count and take top 5
        for key, cnt in sorted(dedup.items(), key=lambda x: x[1], reverse=True)[:5]:
            lines.append(f"- {key}: {cnt}")
    return lines


def get_gchat_events_summary_template(payload: EventsSummaryPayload) -> Dict[str, str]:
    lines: List[str] = [
        f"{Emojis.Source.value} {settings.urls.branding_name} Events Summary - {payload.organization_name}",
        "",
    ]
    # Title

    event_counts = payload.event_counts.data.event_groupings.rows
    events = payload.events_summarised.data.event_groupings.rows
    for acct in payload.accounts.data.accounts:
        counts = next((r for r in event_counts if r.account_id == acct.id), None)
        if not counts or counts.event_count <= 0:
            continue
        acct_lines = create_text_for_account(acct.account_name, acct.id, counts, events)
        lines.extend(acct_lines)
        lines.append("")

    text = "\n".join(lines).strip()
    return {"text": text}
