from typing import Dict, Any, List, Tuple

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    AccountRecommendations,
    DigestRecommendation,
    MAX_ALERT_ITEMS,
    _absolute_url,
    accounts_phrase,
    format_rule_name,
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

    flat = _flatten(params)
    criticals = [(name, rec) for name, rec in flat if rec.severity == "Critical"]
    highs = [(name, rec) for name, rec in flat if rec.severity != "Critical"]

    # Critical-only in the message; top 5 high when there are no criticals.
    if criticals:
        shown = criticals[:MAX_ALERT_ITEMS]
        noun = "critical security finding" if params.critical_count == 1 else "critical security findings"
        headline = f"{params.critical_count} {noun} across {accounts_phrase(account_count)}"
        remaining = params.critical_count - len(shown)
    else:
        shown = highs[:MAX_HIGH_FALLBACK_ITEMS]
        headline = f"Top {len(shown)} high-severity security findings — no criticals today"
        remaining = params.high_count - len(shown)

    context_bits = [f"{settings.urls.branding_name} security alert"]
    context_bits.append(f"Critical {params.critical_count}")
    context_bits.append(f"High {params.high_count} · in dashboard")

    blocks: List[Dict[str, Any]] = [
        {"type": "section", "text": {"type": "mrkdwn", "text": f"*{headline}*"}},
        {"type": "context", "elements": [{"type": "mrkdwn", "text": " · ".join(context_bits)}]},
        {"type": "divider"},
    ]

    for account_name, rec in shown:
        badge = f" `{rec.severity.upper()}`" if rec.severity else ""
        title = f"*{format_rule_name(rec.rule_name)} — {short_resource_name(rec.resource_name)}*{badge}"
        blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": title}})
        blocks.append({"type": "context", "elements": [{"type": "mrkdwn", "text": f"Acct {account_name}"}]})
        if rec.cta_url:
            blocks.append(
                {
                    "type": "actions",
                    "elements": [
                        {
                            "type": "button",
                            "text": {"type": "plain_text", "text": "Details"},
                            "url": _absolute_url(rec.cta_url, base_url),
                        }
                    ],
                }
            )

    if remaining > 0:
        blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": f"_+{remaining} more in the dashboard_"}})

    blocks.append({"type": "divider"})
    if account_count == 1:
        only_account = next(iter(params.recommendations_by_account))
        view_all_url = (
            f"{base_url.rstrip('/')}/kubernetes/details/{only_account}"
            f"?accountId={only_account}&utm=security-alert#security/image-scan"
        )
        blocks.append(
            {
                "type": "actions",
                "elements": [
                    {
                        "type": "button",
                        "text": {"type": "plain_text", "text": "View all findings"},
                        "url": view_all_url,
                        "style": "primary",
                    }
                ],
            }
        )
    else:
        blocks.append(
            {
                "type": "section",
                "text": {"type": "mrkdwn", "text": f"View all findings on {settings.urls.branding_link('slack')}"},
            }
        )
    blocks.append(
        {
            "type": "context",
            "elements": [
                {
                    "type": "mrkdwn",
                    "text": "Only critical findings appear here; when there are none, the top 5 high show instead.",
                }
            ],
        }
    )

    return {
        "text": headline,
        "blocks": blocks[:50],
        "unfurl_links": False,
    }
