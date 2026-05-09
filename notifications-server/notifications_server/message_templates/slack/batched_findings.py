from datetime import datetime
from typing import List, Dict, Any
from pydantic import BaseModel

from notifications_server.configs.settings import settings, URLRoutes


class Account(BaseModel):
    id: str
    account_name: str


class AccountsData(BaseModel):
    accounts: List[Account]


class AccountsDataD(BaseModel):
    data: AccountsData


class BatchedFinding(BaseModel):
    id: str
    title: str
    aggregation_key: str
    severity: str
    count: int
    cluster: str
    subject_name: str
    subject_namespace: str
    account_id: str
    created_at: datetime


class BatchedFindingsPayload(BaseModel):
    organization_id: str
    organization_name: str
    accounts: AccountsDataD
    critical_findings: List[BatchedFinding]
    aggregated_findings: Dict[str, Dict[str, int]]  # account_id -> {aggregation_key -> count}
    total_findings_count: int
    batch_start_time: datetime
    batch_end_time: datetime


def get_batched_findings_message_params(**params) -> BatchedFindingsPayload:
    return BatchedFindingsPayload(**params)


def create_section_block(section_text: str) -> Dict[str, Any]:
    return {"type": "section", "text": {"type": "mrkdwn", "text": section_text.strip()}}


def create_divider_block() -> Dict[str, Any]:
    return {"type": "divider"}


def format_finding_workload(finding: BatchedFinding) -> str:
    workload_info = f"{finding.subject_name}"
    if finding.subject_namespace != "default":
        workload_info += f" ({finding.subject_namespace})"
    return workload_info


def add_critical_findings_blocks(blocks: List[Dict[str, Any]], critical_findings: List[BatchedFinding]) -> None:
    """Add critical findings blocks to the list"""
    if not critical_findings:
        return

    blocks.append(create_section_block("*Top 5 Critical Findings:*"))

    # Sort by count in descending order and take top 5
    top_findings = sorted(critical_findings, key=lambda x: x.count, reverse=True)[:5]

    for finding in top_findings:
        workload = format_finding_workload(finding)
        text = f"• *{finding.title}* – {workload} *({finding.count}x)*"
        blocks.append(create_section_block(text))


def create_fields_block(fields: List[str]) -> Dict[str, Any]:
    return {"type": "section", "fields": [{"type": "mrkdwn", "text": field} for field in fields]}


def add_aggregated_findings_blocks(blocks: List[Dict[str, Any]], aggregated_findings: Dict[str, int]) -> None:
    """Add aggregated findings blocks to the list"""
    if not aggregated_findings:
        return

    blocks.append(create_section_block("*Top 5 Other Findings Summary:*"))

    # Sort by count in descending order and take top 5
    top_findings = sorted(aggregated_findings.items(), key=lambda x: x[1], reverse=True)[:5]

    # Create fields for multi-column layout (2 columns)
    fields = []
    for agg_key, count in top_findings:
        fields.append(f"• {agg_key} *({count})*")

    # Add fields in chunks of 2 for better column layout
    for i in range(0, len(fields), 2):
        chunk = fields[i : i + 2]
        blocks.append(create_fields_block(chunk))


def create_blocks_for_account(
    account_name: str, account_id: str, critical_findings: List[BatchedFinding], aggregated_findings: Dict[str, int]
) -> List[Dict[str, Any]]:
    # Create clickable cluster name that links to events page
    cluster_url = settings.urls.events_url(account_id=account_id, utm_source=URLRoutes.UTMSource.SLACK)
    blocks = [create_section_block(f"*<{cluster_url}|Cluster: {account_name}>*")]

    # Filter critical findings for this account
    account_critical_findings = [f for f in critical_findings if f.account_id == account_id]

    add_critical_findings_blocks(blocks, account_critical_findings)
    add_aggregated_findings_blocks(blocks, aggregated_findings)

    blocks.append(create_divider_block())
    return blocks


def create_header_blocks(payload: BatchedFindingsPayload) -> List[Dict[str, Any]]:
    """Create header blocks for the message"""
    time_range = (
        f"<!date^{int(payload.batch_start_time.timestamp())}^{{time}}|{payload.batch_start_time.strftime('%H:%M')}> -"
        f" <!date^{int(payload.batch_end_time.timestamp())}^{{time}}|{payload.batch_end_time.strftime('%H:%M')}>"
    )

    blocks = [
        create_section_block(f"*Findings Summary - {payload.organization_name}*"),
        create_section_block(f"*Total Findings: {payload.total_findings_count}* | *Period:* {time_range}"),
        create_section_block("Here's your findings summary. Review critical issues that need attention."),
        create_divider_block(),
    ]

    return blocks


def create_account_attachment(
    account_name: str, account_id: str, critical_findings: List[BatchedFinding], aggregated_findings: Dict[str, int]
) -> Dict[str, Any]:
    """Create a collapsible attachment for account findings."""
    blocks = []

    # Filter critical findings for this account
    account_critical_findings = [f for f in critical_findings if f.account_id == account_id]

    # Add critical findings section
    if account_critical_findings:
        blocks.append(create_section_block("*Top 5 Critical Findings:*"))
        top_findings = sorted(account_critical_findings, key=lambda x: x.count, reverse=True)[:5]
        for finding in top_findings:
            workload = format_finding_workload(finding)
            text = f"• *{finding.title}* – {workload} *({finding.count}x)*"
            blocks.append(create_section_block(text))

    # Add aggregated findings section
    if aggregated_findings:
        blocks.append(create_section_block("*Top 5 Other Findings Summary:*"))
        top_agg_findings = sorted(aggregated_findings.items(), key=lambda x: x[1], reverse=True)[:5]
        fields = []
        for agg_key, count in top_agg_findings:
            fields.append(f"• {agg_key} *({count})*")
        for i in range(0, len(fields), 2):
            chunk = fields[i : i + 2]
            blocks.append(create_fields_block(chunk))

    # Calculate fallback text
    critical_count = len(account_critical_findings)
    other_count = sum(aggregated_findings.values())
    fallback = f"{account_name}: {critical_count} critical, {other_count} other findings"

    # Create clickable cluster name that links to events page
    cluster_url = settings.urls.events_url(account_id=account_id, utm_source=URLRoutes.UTMSource.SLACK)

    return {
        "color": "#ff6b6b" if critical_count > 0 else "#ffa500",
        "fallback": fallback,
        "blocks": [create_section_block(f"*<{cluster_url}|Cluster: {account_name}>*")] + blocks,
    }


def get_batched_findings_message_template(payload: BatchedFindingsPayload):
    blocks = create_header_blocks(payload)

    # Get accounts map
    accounts_map = {account.id: account.account_name for account in payload.accounts.data.accounts}

    # Get unique account IDs from findings and aggregated data
    unique_account_ids = {f.account_id for f in payload.critical_findings} | set(payload.aggregated_findings.keys())

    # Create collapsible attachments for each account
    attachments = []
    for account_id in unique_account_ids:
        account_name = accounts_map.get(account_id, "Unknown Account")
        account_aggregated = payload.aggregated_findings.get(account_id, {})
        attachments.append(
            create_account_attachment(account_name, account_id, payload.critical_findings, account_aggregated)
        )

    # Add footer
    blocks.append(create_section_block(f"View more details on {settings.urls.branding_link('slack')}"))

    return {
        "text": f"Findings Summary - {payload.total_findings_count} findings",
        "blocks": blocks,
        "attachments": attachments[:20],  # Slack limit
        "unfurl_links": False,
    }
