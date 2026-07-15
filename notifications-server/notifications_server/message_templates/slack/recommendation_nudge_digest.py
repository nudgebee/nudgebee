from typing import Dict, Any, List, Optional, Tuple

from pydantic import BaseModel

from notifications_server import copy_library
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
    wasted_since_detected: float = 0


class AccountRecommendations(BaseModel):
    account_name: str
    recommendations: List[DigestRecommendation] = []


class NewCounts(BaseModel):
    act_now: int = 0
    critical: int = 0
    high: int = 0


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
    # Delta fields populated by the producer (api-server/services/reports/recommendation_digest.go).
    # Optional so old-payload renders gracefully fall back to today's behaviour minus the totals line.
    new_counts: Optional[NewCounts] = None
    resolved_count: int = 0
    resolved_savings: float = 0
    carryover_count: int = 0
    delta_window_hours: int = 24
    digest_date: str = ""
    # Tenant-wide open savings by category (producer-computed), for header chips
    category_savings: Dict[str, float] = {}


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
    return copy_library.display_name(rule_name)


# Accrued waste below this floor is noise, not urgency — skip the clause.
WASTE_DISPLAY_FLOOR = 10

# One tenant-level alert shows at most this many items; the rest is a count.
MAX_ALERT_ITEMS = 3


# Severity stripe palette, matching the approved preview mocks. Slack renders
# the colored left bar only on attachments, so each item ships as its own
# attachment; headers stay in top-level blocks, which render above attachments,
# and footers ride a neutral-stripe attachment so they land below the items.
STRIPE_CRITICAL = "#C93A36"
STRIPE_HIGH = "#D97A2B"
STRIPE_SAVINGS = "#2C8C55"
STRIPE_NEUTRAL = "#94A3B8"


def severity_stripe(severity: str) -> str:
    return STRIPE_CRITICAL if (severity or "").strip().lower() == "critical" else STRIPE_HIGH


def item_attachment(color: str, fallback: str, blocks: List[Dict[str, Any]]) -> Dict[str, Any]:
    return {"color": color, "fallback": fallback, "blocks": blocks}


def neutral_footer_attachment(blocks: List[Dict[str, Any]], fallback: str = "More") -> Dict[str, Any]:
    return item_attachment(STRIPE_NEUTRAL, fallback, blocks)


def format_waste_clause(wasted: float) -> str:
    if wasted < WASTE_DISPLAY_FLOOR:
        return ""
    return f" · {format_savings(wasted)} wasted since detected"


def accounts_phrase(count: int) -> str:
    return f"{count} account" if count == 1 else f"{count} accounts"


# Long path-style identifiers (ARM IDs, ARNs) drown the message; short k8s
# triplets (ns/Kind/name) stay as-is. Per the spec: short names, IDs hidden.
MAX_RESOURCE_NAME = 60


def short_resource_name(name: str) -> str:
    if not name or len(name) <= MAX_RESOURCE_NAME:
        return name
    tail = name.rsplit("/", 1)[-1] or name
    if len(tail) > MAX_RESOURCE_NAME:
        tail = tail[: MAX_RESOURCE_NAME - 1] + "…"
    return tail


def cost_headline(total_savings: float, account_count: int) -> str:
    return f"You can cut {format_savings(total_savings)}/mo across {accounts_phrase(account_count)}"


def flatten_ranked_recs(
    recommendations_by_account: Dict[str, AccountRecommendations],
) -> List[Tuple[str, DigestRecommendation]]:
    """Flatten the per-account map into one list ordered by band, then score,
    then savings — the alert shows the top items across all accounts."""
    flat: List[Tuple[str, DigestRecommendation]] = []
    for acc_data in recommendations_by_account.values():
        for rec in acc_data.recommendations:
            flat.append((acc_data.account_name, rec))
    band_rank = {band: i for i, band in enumerate(BAND_ORDER)}
    flat.sort(
        key=lambda t: (band_rank.get(t[1].finops_band, len(BAND_ORDER)), -t[1].finops_score, -t[1].estimated_savings)
    )
    return flat


def _absolute_url(url: str, base_url: str) -> str:
    """Slack rejects the whole message when a URL button carries a relative
    URL, so resolve producer-relative CTAs against the notification base."""
    if url.startswith(("http://", "https://")) or not base_url:
        return url
    return f"{base_url.rstrip('/')}/{url.lstrip('/')}"


def build_posture_item_attachments(
    ranked: List[Tuple[str, DigestRecommendation]],
    base_url: str = "",
    color=STRIPE_SAVINGS,
) -> List[Dict[str, Any]]:
    """One colored attachment per item, in priority order: what & money first,
    then the small facts/identity line, then the action. `color` is a hex
    string or a callable(rec) -> hex."""
    attachments: List[Dict[str, Any]] = []
    for account_name, rec in ranked[:MAX_ALERT_ITEMS]:
        item_blocks: List[Dict[str, Any]] = []
        title = f"{format_rule_name(rec.rule_name)} — {short_resource_name(rec.resource_name)}"
        lines = [f"*{title}*"]
        if rec.estimated_savings > 0:
            lines.append(
                f"Save *{format_savings(rec.estimated_savings)}/mo*{format_waste_clause(rec.wasted_since_detected)}"
            )
        item_blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": "\n".join(lines)}})

        band_display = BAND_DISPLAY_NAMES.get(rec.finops_band, rec.finops_band)
        facts = f"{band_display} · Score {rec.finops_score}/100 · {rec.severity} · Acct {account_name}"
        item_blocks.append({"type": "context", "elements": [{"type": "mrkdwn", "text": facts}]})

        if rec.cta_url:
            item_blocks.append(
                {
                    "type": "actions",
                    "elements": [
                        {
                            "type": "button",
                            "text": {"type": "plain_text", "text": "Details"},
                            "style": "primary",
                            "url": _absolute_url(rec.cta_url, base_url),
                        }
                    ],
                }
            )

        stripe = color(rec) if callable(color) else color
        attachments.append(item_attachment(stripe, title, item_blocks))
    return attachments


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

    # Header: savings-first headline when there is money on the table; digests
    # without savings (e.g. security-only) keep the plain title.
    if params.total_recoverable_savings > 0:
        headline = cost_headline(params.total_recoverable_savings, len(params.recommendations_by_account))
        blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": f"*{headline}*"}})
        branding_line = f"{settings.urls.branding_name} daily brief"
        if params.digest_date:
            branding_line += f" · {params.digest_date}"
        blocks.append({"type": "context", "elements": [{"type": "mrkdwn", "text": branding_line}]})
    else:
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

    summary_lines = _build_summary_lines(params)
    if summary_lines:
        blocks.append(
            {
                "type": "section",
                "text": {"type": "mrkdwn", "text": "\n".join(summary_lines)},
            }
        )
        blocks.append({"type": "divider"})

    # Top items across all accounts, priority-ordered, capped, one colored
    # attachment per item (the severity/savings stripe from the design mocks)
    ranked = flatten_ranked_recs(params.recommendations_by_account)
    attachments = build_posture_item_attachments(ranked, base_url)

    # Footer rides a neutral attachment so it renders below the items.
    # utm=slack-digest distinguishes digest clicks from other Slack
    # notifications; digest_date lets click-through analytics correlate
    # clicks back to a specific brief.
    footer_url = f"{base_url}/optimise?utm=slack-digest"
    if params.digest_date:
        footer_url += f"&d={params.digest_date}"
    footer_url += "#recommendations"
    footer_blocks: List[Dict[str, Any]] = []
    remaining = len(ranked) - MAX_ALERT_ITEMS
    if remaining > 0:
        footer_blocks.append(
            {"type": "section", "text": {"type": "mrkdwn", "text": f"_+{remaining} more in the dashboard_"}}
        )
    footer_blocks.append(
        {
            "type": "actions",
            "elements": [
                {
                    "type": "button",
                    "text": {
                        "type": "plain_text",
                        "text": "View All Recommendations",
                    },
                    "url": footer_url,
                    "style": "primary",
                }
            ],
        }
    )
    attachments.append(neutral_footer_attachment(footer_blocks, "View all recommendations"))

    return {
        "text": f"{params.title} - {format_savings(params.total_recoverable_savings)}/mo recoverable",
        "blocks": blocks[:50],
        "attachments": attachments[:20],
        "unfurl_links": False,
    }


def _build_summary_lines(params: RecommendationNudgeDigestParams) -> List[str]:
    """Header lines below the headline. Order: in-brief band counts, NEW counts,
    resolved + carryover.

    Lines are emitted conditionally: quiet days don't get an empty 'new' line,
    and old-payload renders (new_counts is None) fall back to today's behaviour
    minus the misleading top-20 band totals. Recoverable savings live in the
    headline, not here.
    """
    lines: List[str] = []

    brief_parts: List[str] = []
    if params.act_now_count:
        brief_parts.append(f"{params.act_now_count} Priority")
    if params.critical_count:
        brief_parts.append(f"{params.critical_count} Critical")
    if params.high_count:
        brief_parts.append(f"{params.high_count} High")
    if brief_parts:
        lines.append("*In this brief:* " + " · ".join(brief_parts))

    if params.category_savings:
        top_categories = sorted(params.category_savings.items(), key=lambda kv: kv[1], reverse=True)[:4]
        chips = " · ".join(
            f"{copy_library.category_display_name(cat)} {format_savings(savings)}"
            for cat, savings in top_categories
            if savings > 0
        )
        if chips:
            lines.append(f"*By category:* {chips}")

    if params.new_counts is not None:
        parts: List[str] = []
        if params.new_counts.act_now:
            parts.append(f"{params.new_counts.act_now} Priority")
        if params.new_counts.critical:
            parts.append(f"{params.new_counts.critical} Critical")
        if params.new_counts.high:
            parts.append(f"{params.new_counts.high} High")
        if parts:
            lines.append("*New since yesterday:* " + " · ".join(parts))
        else:
            lines.append("_No new recommendations since yesterday._")

    status_bits: List[str] = []
    if params.resolved_count:
        suffix = f" (saved {format_savings(params.resolved_savings)}/mo)" if params.resolved_savings > 0 else ""
        status_bits.append(f"*{params.resolved_count} resolved*{suffix}")
    if params.carryover_count:
        status_bits.append(f"{params.carryover_count} still open from earlier")
    if status_bits:
        lines.append(" · ".join(status_bits))

    return lines
