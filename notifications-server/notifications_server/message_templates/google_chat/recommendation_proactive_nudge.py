from typing import Dict, Any, List

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_proactive_nudge import (
    ProactiveNudgeParams,
)
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    format_rule_name,
    format_savings,
)


def get_gchat_recommendation_proactive_nudge_template(
    params: ProactiveNudgeParams,
) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    branding = settings.urls.branding_name
    lines: List[str] = []

    lines.append(f"*{branding} FinOps Alert — Priority*")
    lines.append("-" * 25)
    lines.append(f"{params.total_recommendations} recommendations require immediate action")
    lines.append(f"Total recoverable: *{format_savings(params.total_recoverable_savings)}/mo*")
    lines.append("")

    # Recommendations grouped by account
    counter = 1
    for _acc_id, acc_data in params.recommendations_by_account.items():
        lines.append(f"*{acc_data.account_name}*")
        for rec in acc_data.recommendations[:5]:
            lines.append(f"  {counter}. *{rec.resource_name}* \u2014 {format_rule_name(rec.rule_name)}")
            lines.append(
                f"     Score: {rec.finops_score}/100 \u00b7 "
                f"Savings: {format_savings(rec.estimated_savings)}/mo \u00b7 "
                f"Severity: {rec.severity}"
            )
            counter += 1

        remaining = len(acc_data.recommendations) - 5
        if remaining > 0:
            lines.append(f"  _and {remaining} more..._")
        lines.append("")

    lines.append(f"View all recommendations: {base_url}/optimize/summary?utm=gchat")
    lines.append(f"Ask Nubi: {base_url}/chat")

    return {"text": "\n".join(lines)}
