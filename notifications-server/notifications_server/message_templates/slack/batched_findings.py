from datetime import datetime
from typing import List, Dict, Any
from pydantic import BaseModel

from notifications_server import copy_library
from notifications_server.configs.settings import settings, URLRoutes
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    MAX_ALERT_ITEMS,
    STRIPE_CRITICAL,
    accounts_phrase,
    accounts_scope,
    header_block,
    legacy_attachment,
    link_button,
    neutral_footer_attachment,
)


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
    critical_count: int = 0  # true distinct-critical count; critical_findings is capped
    aggregated_findings: Dict[str, Dict[str, int]]  # account_id -> {aggregation_key -> count}
    total_findings_count: int
    batch_start_time: datetime
    batch_end_time: datetime


def get_batched_findings_message_params(**params) -> BatchedFindingsPayload:
    return BatchedFindingsPayload(**params)


def create_section_block(section_text: str) -> Dict[str, Any]:
    return {"type": "section", "text": {"type": "mrkdwn", "text": section_text.strip()}}


def create_context_block(text: str) -> Dict[str, Any]:
    return {"type": "context", "elements": [{"type": "mrkdwn", "text": text}]}


def create_divider_block() -> Dict[str, Any]:
    return {"type": "divider"}


def format_finding_workload(finding: BatchedFinding) -> str:
    workload_info = f"{finding.subject_name}"
    if finding.subject_namespace != "default":
        workload_info += f" ({finding.subject_namespace})"
    return workload_info


TOP_AGGREGATED = 5


def _time_range(payload: BatchedFindingsPayload) -> str:
    return (
        f"<!date^{int(payload.batch_start_time.timestamp())}^{{time}}|{payload.batch_start_time.strftime('%H:%M')}> -"
        f" <!date^{int(payload.batch_end_time.timestamp())}^{{time}}|{payload.batch_end_time.strftime('%H:%M')}>"
    )


def _merged_aggregated(payload: BatchedFindingsPayload) -> List:
    merged: Dict[str, int] = {}
    for agg_map in payload.aggregated_findings.values():
        for key, count in agg_map.items():
            merged[key] = merged.get(key, 0) + count
    return sorted(merged.items(), key=lambda kv: kv[1], reverse=True)[:TOP_AGGREGATED]


def get_batched_findings_message_template(payload: BatchedFindingsPayload):
    accounts_map = {account.id: account.account_name for account in payload.accounts.data.accounts}
    unique_account_ids = {f.account_id for f in payload.critical_findings} | set(payload.aggregated_findings.keys())
    account_count = len(unique_account_ids) or len(accounts_map)

    criticals = sorted(payload.critical_findings, key=lambda f: f.count, reverse=True)

    scope = (
        accounts_scope([accounts_map.get(acc_id, acc_id) for acc_id in unique_account_ids])
        if unique_account_ids
        else f"across {accounts_phrase(account_count)}"
    )

    # critical_count is the true distinct-critical total (the list is capped).
    # Fall back to a count-free headline for payloads predating the field.
    if payload.critical_count > 0:
        noun = "critical finding" if payload.critical_count == 1 else "critical findings"
        headline = f"{payload.critical_count} {noun} {scope}"
    elif criticals:
        headline = f"Top critical findings {scope}"
    else:
        headline = f"{payload.total_findings_count} findings {scope}"

    blocks: List[Dict[str, Any]] = [
        header_block(headline),
        create_context_block(
            f"{payload.organization_name} findings summary · "
            f"{payload.total_findings_count} findings between {_time_range(payload)}"
        ),
        create_divider_block(),
    ]

    attachments = []
    shown = criticals[:MAX_ALERT_ITEMS]
    for finding in shown:
        account_name = accounts_map.get(finding.account_id, finding.account_id)
        facts = (
            f"{finding.severity.title()} priority · {format_finding_workload(finding)} · "
            f"{finding.cluster} · Acct: {account_name}"
        )
        details_url = settings.urls.investigate_url(
            finding.account_id, finding.id, utm_source=URLRoutes.UTMSource.SLACK
        )
        attachments.append(
            legacy_attachment(
                STRIPE_CRITICAL,
                finding.title,
                text=f"*{finding.title}*\n*{finding.count}×* in the window\n{facts}",
                actions=[link_button("Details", details_url, style="primary")],
            )
        )

    footer_lines = []
    remaining = payload.total_findings_count - sum(f.count for f in shown)
    if remaining > 0:
        footer_lines.append(f"_+{remaining} more findings in the window_")

    top_aggregated = _merged_aggregated(payload)
    if top_aggregated:
        footer_lines.append(
            "Also seen: " + " · ".join(f"{copy_library.display_name(key)} ({count})" for key, count in top_aggregated)
        )

    footer_actions = None
    if len(unique_account_ids) == 1:
        only_account = next(iter(unique_account_ids))
        footer_actions = [
            link_button(
                "View all findings",
                settings.urls.events_url(only_account, utm_source=URLRoutes.UTMSource.SLACK),
                style="primary",
            )
        ]
    else:
        footer_lines.append(f"View all findings on {settings.urls.branding_link('slack')}")
    attachments.append(
        neutral_footer_attachment(
            text="\n".join(footer_lines),
            actions=footer_actions,
            fallback="View all findings",
        )
    )

    return {
        "text": f"{headline} — {payload.total_findings_count} findings",
        "blocks": blocks[:50],
        "attachments": attachments[:20],
        "unfurl_links": False,
    }
