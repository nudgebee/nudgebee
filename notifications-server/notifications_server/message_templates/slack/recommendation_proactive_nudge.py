from typing import Dict, Any, List, Tuple

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    AccountRecommendations,
    DigestRecommendation,
    accounts_phrase,
    append_posture_item_blocks,
    cost_headline,
    flatten_ranked_recs,
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


def build_ask_nubi_url(params: ProactiveNudgeParams, base_url: str, utm: str) -> str:
    """Deep-link to the Nubi assistant page (``/ask-nudgebee``).

    Shared by the Slack/Teams/Google-Chat proactive-nudge templates so the
    destination can't drift between channels. When the bundle covers exactly
    one account we pass ``accountId`` so Nubi opens scoped to that account's
    context; with multiple accounts there's no single one to pick, so we link
    to the generic assistant page.
    """
    url = f"{base_url}/ask-nudgebee?utm={utm}"
    if len(params.recommendations_by_account) == 1:
        account_id = next(iter(params.recommendations_by_account))
        url += f"&accountId={account_id}"
    return url


def get_recommendation_proactive_nudge_message_template(
    params: ProactiveNudgeParams,
) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    branding = settings.urls.branding_name
    blocks: List[Dict[str, Any]] = []

    # Header: savings-first headline, branding demoted to a context line.
    # All-zero-savings bundles fall back to a count headline instead of "$0.00/mo".
    account_count = len(params.recommendations_by_account)
    if params.total_recoverable_savings > 0:
        headline = cost_headline(params.total_recoverable_savings, account_count)
        context_line = f"{branding} priority alert · {params.total_recommendations} recommendations need action now"
    else:
        headline = (
            f"{params.total_recommendations} priority recommendations need action "
            f"across {accounts_phrase(account_count)}"
        )
        context_line = f"{branding} priority alert"
    blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": f"*{headline}*"}})
    blocks.append({"type": "context", "elements": [{"type": "mrkdwn", "text": context_line}]})
    blocks.append({"type": "divider"})

    # Top items across all accounts, priority-ordered, capped
    append_posture_item_blocks(blocks, flatten_ranked_recs(params.recommendations_by_account), base_url)

    # Footer actions
    blocks.append({"type": "divider"})
    blocks.append(
        {
            "type": "actions",
            "elements": [
                {
                    "type": "button",
                    "text": {"type": "plain_text", "text": "View All Recommendations"},
                    "url": f"{base_url}/optimise?utm=slack#recommendations",
                    "style": "primary",
                },
                {
                    "type": "button",
                    "text": {"type": "plain_text", "text": "Ask Nubi"},
                    "url": build_ask_nubi_url(params, base_url, "slack"),
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
