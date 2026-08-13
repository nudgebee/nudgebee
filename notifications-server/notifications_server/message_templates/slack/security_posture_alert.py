from typing import Dict, Any, List, Tuple

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    AccountRecommendations,
    DigestRecommendation,
    MAX_ALERT_ITEMS,
    _absolute_url,
    accounts_scope,
    format_rule_name,
    header_block,
    legacy_attachment,
    link_button,
    neutral_footer_attachment,
    severity_stripe,
    short_resource_name,
)

# When there are no criticals, the spec shows the top 5 high instead.
MAX_HIGH_FALLBACK_ITEMS = 5


class SecurityPostureAlertParams(BaseModel):
    organization_id: str = ""
    organization_name: str = ""
    critical_count: int = 0
    high_count: int = 0
    recommendations_by_account: Dict[str, AccountRecommendations] = {}
    base_url: str = ""


def get_security_posture_alert_message_params(**params) -> SecurityPostureAlertParams:
    raw_by_account = params.get("recommendations_by_account", {})
    parsed = {}
    for acc_id, acc_data in raw_by_account.items():
        if isinstance(acc_data, dict):
            parsed[acc_id] = AccountRecommendations(**acc_data)
        else:
            parsed[acc_id] = acc_data
    params["recommendations_by_account"] = parsed
    return SecurityPostureAlertParams(**params)


def _flatten(params: SecurityPostureAlertParams) -> List[Tuple[str, DigestRecommendation]]:
    flat: List[Tuple[str, DigestRecommendation]] = []
    for acc_data in params.recommendations_by_account.values():
        for rec in acc_data.recommendations:
            flat.append((acc_data.account_name, rec))
    flat.sort(key=lambda t: (0 if t[1].severity == "Critical" else 1, -t[1].finops_score))
    return flat


def get_security_posture_alert_message_template(params: SecurityPostureAlertParams) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    account_count = len(params.recommendations_by_account)
    scope = accounts_scope([acc.account_name for acc in params.recommendations_by_account.values()])

    flat = _flatten(params)
    criticals = [(name, rec) for name, rec in flat if rec.severity == "Critical"]
    highs = [(name, rec) for name, rec in flat if rec.severity != "Critical"]

    # Critical-only in the message; top 5 high when there are no criticals.
    if criticals:
        shown = criticals[:MAX_ALERT_ITEMS]
        noun = "critical security finding" if params.critical_count == 1 else "critical security findings"
        headline = f"{params.critical_count} {noun} {scope}"
        remaining = params.critical_count - len(shown)
    else:
        shown = highs[:MAX_HIGH_FALLBACK_ITEMS]
        headline = f"Top {len(shown)} high-severity security findings — no criticals today"
        remaining = params.high_count - len(shown)

    if criticals:
        shown_phrase = "These are" if len(shown) > 1 else "This is"
        intro = (
            f"{shown_phrase} worth interrupting you for. "
            f"{params.high_count} high-severity findings are queued in the dashboard and don't need action today."
            if params.high_count > 0
            else f"{shown_phrase} worth interrupting you for."
        )
    else:
        intro = "Nothing critical. These are the highest-severity open items if you have capacity this week."

    context_bits = [f"{settings.urls.branding_name} security alert"]
    context_bits.append(f"Critical {params.critical_count}")
    context_bits.append(f"High {params.high_count} · in dashboard")
    context_bits.append(f"Accounts {account_count}")

    blocks: List[Dict[str, Any]] = [
        header_block(headline),
        {"type": "section", "text": {"type": "mrkdwn", "text": intro}},
        {"type": "context", "elements": [{"type": "mrkdwn", "text": " · ".join(context_bits)}]},
        {"type": "divider"},
    ]

    attachments = []
    for account_name, rec in shown:
        badge = f" `{rec.severity.upper()}`" if rec.severity else ""
        title = f"{format_rule_name(rec.rule_name)} — {short_resource_name(rec.resource_name)}"
        actions = None
        if rec.cta_url:
            actions = [link_button("Details", _absolute_url(rec.cta_url, base_url), style="primary")]
        attachments.append(
            legacy_attachment(
                severity_stripe(rec.severity),
                title,
                text=f"*{title}*{badge}\nAcct: {account_name}",
                actions=actions,
            )
        )

    total_findings = params.critical_count + params.high_count
    view_all_label = f"View all {total_findings} findings" if total_findings > 1 else "View all findings"

    footer_lines = []
    if remaining > 0:
        footer_lines.append(f"_+{remaining} more in the dashboard_")
    footer_actions = None
    if account_count == 1:
        only_account = next(iter(params.recommendations_by_account))
        view_all_url = (
            f"{base_url.rstrip('/')}/kubernetes/details/{only_account}"
            f"?accountId={only_account}&utm=security-alert#security/image-scan"
        )
        footer_actions = [link_button(view_all_label, view_all_url, style="primary")]
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
        "text": headline,
        "blocks": blocks[:50],
        "attachments": attachments[:20],
        "unfurl_links": False,
    }
