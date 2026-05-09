from typing import Dict, Any, List, Tuple

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    AccountRecommendations,
    DigestRecommendation,
    format_rule_name,
    format_savings,
)


class ProactiveNudgeParams(BaseModel):
    organization_id: str = ""
    organization_name: str = ""
    total_recommendations: int = 0
    total_recoverable_savings: float = 0
    recommendations_by_account: Dict[str, AccountRecommendations] = {}
    base_url: str = ""


def get_recommendation_proactive_nudge_message_params(
    **params,
) -> ProactiveNudgeParams:
    raw_by_account = params.get("recommendations_by_account", {})
    parsed = {}
    for acc_id, acc_data in raw_by_account.items():
        if isinstance(acc_data, dict):
            parsed[acc_id] = AccountRecommendations(**acc_data)
        else:
            parsed[acc_id] = acc_data
    params["recommendations_by_account"] = parsed
    return ProactiveNudgeParams(**params)


def get_recommendation_proactive_nudge_message_template(
    params: ProactiveNudgeParams,
) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    branding = settings.urls.branding_name
    blocks: List[Dict[str, Any]] = []

    # Header
    blocks.append(
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": f"*{branding} FinOps Alert — Priority*",
            },
        }
    )
    blocks.append({"type": "divider"})

    # Summary
    summary = (
        f"{params.total_recommendations} recommendations require immediate action\n"
        f"Total recoverable: *{format_savings(params.total_recoverable_savings)}/mo*"
    )
    blocks.append(
        {
            "type": "section",
            "text": {"type": "mrkdwn", "text": summary},
        }
    )
    blocks.append({"type": "divider"})

    # Recommendations grouped by account
    counter = 1
    for _acc_id, acc_data in params.recommendations_by_account.items():
        blocks.append(
            {
                "type": "section",
                "text": {
                    "type": "mrkdwn",
                    "text": f"*{acc_data.account_name}*",
                },
            }
        )
        for rec in acc_data.recommendations[:5]:
            rec_text = (
                f"{counter}. *{rec.resource_name}* — {format_rule_name(rec.rule_name)}\n"
                f"    Score: {rec.finops_score}/100 · "
                f"Savings: {format_savings(rec.estimated_savings)}/mo · "
                f"Severity: {rec.severity} · Category: {rec.category}"
            )
            if rec.cta_url:
                rec_text += f"  <{rec.cta_url}|Review & Act>"
            blocks.append(
                {
                    "type": "section",
                    "text": {"type": "mrkdwn", "text": rec_text},
                }
            )
            counter += 1

        remaining = len(acc_data.recommendations) - 5
        if remaining > 0:
            blocks.append(
                {
                    "type": "section",
                    "text": {
                        "type": "mrkdwn",
                        "text": f"  _and {remaining} more..._",
                    },
                }
            )

    # Footer actions
    blocks.append({"type": "divider"})
    blocks.append(
        {
            "type": "actions",
            "elements": [
                {
                    "type": "button",
                    "text": {"type": "plain_text", "text": "View All Recommendations"},
                    "url": f"{base_url}/optimize/summary?utm=slack",
                    "style": "primary",
                },
                {
                    "type": "button",
                    "text": {"type": "plain_text", "text": "Ask Nubi"},
                    "url": f"{base_url}/chat",
                },
            ],
        }
    )

    fallback = (
        f"Priority: {params.total_recommendations} recommendations — "
        f"{format_savings(params.total_recoverable_savings)}/mo recoverable"
    )

    return {
        "text": fallback,
        "blocks": blocks[:50],
        "unfurl_links": False,
    }
