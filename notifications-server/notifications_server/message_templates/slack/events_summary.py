from datetime import datetime
from typing import List, Dict, Any
from pydantic import BaseModel

from notifications_server.configs.settings import settings


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


def get_events_summary_message_params(**params) -> EventsSummaryPayload:
    return EventsSummaryPayload(**params)


def create_markdown_block(text: str) -> Dict[str, Any]:
    return {"type": "mrkdwn", "text": text.strip()}


def create_section_block(text: str) -> Dict[str, Any]:
    return {"type": "section", "text": create_markdown_block(text)}


def create_fields_block(fields: List[str]) -> Dict[str, Any]:
    return {
        "type": "section",
        "fields": [create_markdown_block(field) for field in fields],
    }


def create_attachment_block(fields: List[str]) -> Dict[str, Any]:
    return {
        "type": "context",
        "elements": [create_markdown_block(field) for field in fields],
    }


def create_divider_block() -> Dict[str, Any]:
    return {"type": "divider"}


def create_blocks_for_account(
    account_name: str, account_id: str, event_counts: List[EventCountRow], events_summarised: List[EventSummarisedRow]
) -> List[Dict[str, Any]]:
    blocks = [create_section_block(f"*Account Name:* {account_name}")]

    # Header for Account Name

    # Event Counts
    account_event_counts = next((row for row in event_counts if row.account_id == account_id), None)
    if account_event_counts:
        blocks.append(create_section_block(f"*Total Events:* {account_event_counts.event_count}"))

        blocks.append(create_section_block("*By Severity:*"))
        blocks.append(
            create_attachment_block(
                [
                    f" *High Priority:* {account_event_counts.count_priority_high}",
                    f" *Medium Priority:* {account_event_counts.count_priority_medium}",
                    f" *Low Priority:* {account_event_counts.count_priority_low}",
                ]
            )
        )

        blocks.append(create_section_block("*By Type:*"))
        blocks.append(
            create_attachment_block(
                [
                    f"*Application Issues:* {account_event_counts.count_application_issues}\n",
                    f"*Node Issues:* {account_event_counts.count_node_issues}\n",
                    f"*Pod Issues:* {account_event_counts.count_pod_issues}\n",
                ]
            )
        )

    # Top Events (deduplicate by aggregation_key within account, top 5)
    summarised_events = [event for event in events_summarised if event.account_id == account_id]
    if summarised_events:
        # Deduplicate by aggregation_key and sum event counts
        dedup: Dict[str, int] = {}
        for event in summarised_events:
            dedup[event.aggregation_key] = dedup.get(event.aggregation_key, 0) + event.event_count

        blocks.append(create_section_block("*Top Events:*"))

        # Sort by count and take top 5
        sorted_events = sorted(dedup.items(), key=lambda x: x[1], reverse=True)[:5]

        fields = []
        for aggregation_key, count in sorted_events:
            fields.append(f"*{aggregation_key}:* {count} events")

        # Slack has a limit of 10 items per fields array, so split into chunks
        for i in range(0, len(fields), 10):
            chunk = fields[i : i + 10]
            blocks.append(create_fields_block(chunk))

    # Divider
    blocks.append(create_divider_block())

    return blocks


def create_account_attachment(
    account_name: str, account_id: str, event_counts: List[EventCountRow], events_summarised: List[EventSummarisedRow]
) -> Dict[str, Any]:
    """Create a collapsible attachment for account event data."""
    blocks = []

    # Event Counts
    account_event_counts = next((row for row in event_counts if row.account_id == account_id), None)
    total_events = 0
    if account_event_counts:
        total_events = account_event_counts.event_count
        blocks.append(create_section_block(f"*Total Events:* {account_event_counts.event_count}"))

        blocks.append(create_section_block("*By Severity:*"))
        blocks.append(
            create_attachment_block(
                [
                    f" *High Priority:* {account_event_counts.count_priority_high}",
                    f" *Medium Priority:* {account_event_counts.count_priority_medium}",
                    f" *Low Priority:* {account_event_counts.count_priority_low}",
                ]
            )
        )

        blocks.append(create_section_block("*By Type:*"))
        blocks.append(
            create_attachment_block(
                [
                    f"*Application Issues:* {account_event_counts.count_application_issues}\n",
                    f"*Node Issues:* {account_event_counts.count_node_issues}\n",
                    f"*Pod Issues:* {account_event_counts.count_pod_issues}\n",
                ]
            )
        )

    # Top Events
    summarised_events = [event for event in events_summarised if event.account_id == account_id]
    top_events_count = 0
    if summarised_events:
        dedup: Dict[str, int] = {}
        for event in summarised_events:
            dedup[event.aggregation_key] = dedup.get(event.aggregation_key, 0) + event.event_count

        blocks.append(create_section_block("*Top Events:*"))
        sorted_events = sorted(dedup.items(), key=lambda x: x[1], reverse=True)[:5]
        top_events_count = len(sorted_events)

        fields = []
        for aggregation_key, count in sorted_events:
            fields.append(f"*{aggregation_key}:* {count} events")

        for i in range(0, len(fields), 10):
            chunk = fields[i : i + 10]
            blocks.append(create_fields_block(chunk))

    # Fallback text for collapsed view
    fallback = f"{account_name}: {total_events} total events"
    if top_events_count > 0:
        fallback += f", {top_events_count} event types"

    return {
        "color": "#2196F3",
        "fallback": fallback,
        "blocks": [create_section_block(f"*Account Name:* {account_name}")] + blocks,
    }


def get_events_summary_message_template(payload: EventsSummaryPayload):
    blocks = [
        create_section_block(f"*{settings.urls.branding_name} Events Summary* - {payload.organization_name}"),
        create_divider_block(),
    ]

    event_counts = payload.event_counts.data.event_groupings.rows
    events_summarised = payload.events_summarised.data.event_groupings.rows
    accounts_map = {account.id: account.account_name for account in payload.accounts.data.accounts}

    unique_account_ids = {row.account_id for row in event_counts} | {row.account_id for row in events_summarised}

    # Create collapsible attachments for each account
    attachments = []
    for account_id in unique_account_ids:
        account_name = accounts_map.get(account_id, "Unknown Account")
        attachments.append(create_account_attachment(account_name, account_id, event_counts, events_summarised))

    return {
        "text": "Events Summary",
        "blocks": blocks,
        "attachments": attachments[:20],  # Slack limit
        "unfurl_links": False,
    }
