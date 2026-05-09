from typing import Dict, Any, List, Tuple

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings

BAND_ORDER = ["Act Now", "Critical", "High"]

BAND_DISPLAY_NAMES = {
    "Act Now": "Priority",
    "Critical": "Critical",
    "High": "High",
}


class DigestRecommendation(BaseModel):
    id: str
    rule_name: str
    resource_name: str
    finops_score: int
    finops_band: str
    estimated_savings: float = 0
    severity: str = "Medium"
    category: str = ""
    cta_url: str = ""


class AccountRecommendations(BaseModel):
    account_name: str
    recommendations: List[DigestRecommendation] = []


class RecommendationNudgeDigestParams(BaseModel):
    organization_id: str = ""
    organization_name: str = ""
    title: str = "FinOps Daily Brief"
    total_recoverable_savings: float = 0
    act_now_count: int = 0
    critical_count: int = 0
    high_count: int = 0
    recommendations_by_account: Dict[str, AccountRecommendations] = {}
    base_url: str = ""


def get_recommendation_nudge_digest_message_params(
    **params,
) -> RecommendationNudgeDigestParams:
    raw_by_account = params.get("recommendations_by_account", {})
    parsed = {}
    for acc_id, acc_data in raw_by_account.items():
        if isinstance(acc_data, dict):
            parsed[acc_id] = AccountRecommendations(**acc_data)
        else:
            parsed[acc_id] = acc_data
    params["recommendations_by_account"] = parsed
    return RecommendationNudgeDigestParams(**params)


def format_savings(amount: float) -> str:
    if amount >= 1000:
        return f"${amount:,.0f}"
    return f"${amount:.2f}"


def format_rule_name(rule_name: str) -> str:
    return rule_name.replace("_", " ").replace("-", " ").title()


def collect_recs_by_band(
    params: RecommendationNudgeDigestParams,
) -> Dict[str, List[Tuple[str, DigestRecommendation]]]:
    """Group recommendations by band across all accounts."""
    result: Dict[str, List[Tuple[str, DigestRecommendation]]] = {band: [] for band in BAND_ORDER}
    for acc_data in params.recommendations_by_account.values():
        for rec in acc_data.recommendations:
            if rec.finops_band in result:
                result[rec.finops_band].append((acc_data.account_name, rec))
    return result


def get_recommendation_nudge_digest_message_template(
    params: RecommendationNudgeDigestParams,
) -> Dict[str, Any]:
    base_url = params.base_url or public_ip()
    blocks: List[Dict[str, Any]] = []

    # Header
    blocks.append(
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": f"*{settings.urls.branding_name} {params.title}*",
            },
        }
    )
    blocks.append({"type": "divider"})

    # Summary line
    summary_parts = [f"*{format_savings(params.total_recoverable_savings)}/mo recoverable*"]
    if params.act_now_count > 0:
        summary_parts.append(f"{params.act_now_count} Priority")
    if params.critical_count > 0:
        summary_parts.append(f"{params.critical_count} Critical")
    if params.high_count > 0:
        summary_parts.append(f"{params.high_count} High")

    blocks.append(
        {
            "type": "section",
            "text": {"type": "mrkdwn", "text": " · ".join(summary_parts)},
        }
    )
    blocks.append({"type": "divider"})

    # Recommendations grouped by band
    recs_by_band = collect_recs_by_band(params)
    for band in BAND_ORDER:
        band_recs = recs_by_band[band]
        if not band_recs:
            continue
        display_name = BAND_DISPLAY_NAMES.get(band, band)
        blocks.append(
            {
                "type": "section",
                "text": {"type": "mrkdwn", "text": f"*{display_name}*"},
            }
        )
        _append_slack_rec_blocks(blocks, band_recs)

    # Footer with CTA
    blocks.append({"type": "divider"})
    blocks.append(
        {
            "type": "actions",
            "elements": [
                {
                    "type": "button",
                    "text": {
                        "type": "plain_text",
                        "text": "View All Recommendations",
                    },
                    "url": f"{base_url}/optimize/summary?utm=slack",
                    "style": "primary",
                }
            ],
        }
    )

    return {
        "text": f"{params.title} - {format_savings(params.total_recoverable_savings)}/mo recoverable",
        "blocks": blocks[:50],
        "unfurl_links": False,
    }


def _append_slack_rec_blocks(
    blocks: List[Dict[str, Any]],
    band_recs: List[Tuple[str, DigestRecommendation]],
) -> None:
    """Append up to 5 recommendation blocks plus overflow text."""
    for _account_name, rec in band_recs[:5]:
        savings_text = f" — {format_savings(rec.estimated_savings)}/mo" if rec.estimated_savings > 0 else ""
        rec_text = f"• *{rec.resource_name}* {format_rule_name(rec.rule_name)}{savings_text}  <{rec.cta_url}|Review>"
        blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": rec_text}})

    remaining = len(band_recs) - 5
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
