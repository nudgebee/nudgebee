from typing import Dict, Any, List

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_proactive_nudge import (
    ProactiveNudgeParams,
    build_ask_nubi_url,
)
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    format_rule_name,
    format_savings,
)


def get_teams_recommendation_proactive_nudge_template(
    params: ProactiveNudgeParams,
) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    branding = settings.urls.branding_name
    body: List[Dict[str, Any]] = []

    # Header
    body.append(
        {
            "type": "TextBlock",
            "text": f"{branding} FinOps Alert — Priority",
            "size": "Large",
            "weight": "Bolder",
            "color": "Attention",
            "wrap": True,
        }
    )

    # Summary facts
    body.append(
        {
            "type": "FactSet",
            "facts": [
                {
                    "title": "Recommendations",
                    "value": str(params.total_recommendations),
                },
                {
                    "title": "Total Recoverable",
                    "value": f"{format_savings(params.total_recoverable_savings)}/mo",
                },
            ],
        }
    )
    body.append({"type": "TextBlock", "text": "---", "separator": True})

    # Recommendations grouped by account
    counter = 1
    for _acc_id, acc_data in params.recommendations_by_account.items():
        body.append(
            {
                "type": "TextBlock",
                "text": f"**{acc_data.account_name}**",
                "weight": "Bolder",
                "wrap": True,
            }
        )
        for rec in acc_data.recommendations[:5]:
            rec_text = (
                f"{counter}. **{rec.resource_name}** — {format_rule_name(rec.rule_name)}\n"
                f"Score: {rec.finops_score}/100 · "
                f"Savings: {format_savings(rec.estimated_savings)}/mo · "
                f"Severity: {rec.severity}"
            )
            body.append(
                {
                    "type": "TextBlock",
                    "text": rec_text,
                    "wrap": True,
                }
            )
            counter += 1

        remaining = len(acc_data.recommendations) - 5
        if remaining > 0:
            body.append(
                {
                    "type": "TextBlock",
                    "text": f"_and {remaining} more..._",
                    "isSubtle": True,
                    "wrap": True,
                }
            )

    return {
        "$schema": "https://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.2",
        "msteams": {"width": "Full"},
        "body": body,
        "actions": [
            {
                "type": "Action.OpenUrl",
                "title": "View All Recommendations",
                "url": f"{base_url}/optimise?utm=teams#recommendations",
            },
            {
                "type": "Action.OpenUrl",
                "title": "Ask Nubi",
                "url": build_ask_nubi_url(params, base_url, "teams"),
            },
        ],
    }
