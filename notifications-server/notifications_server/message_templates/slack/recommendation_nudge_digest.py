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
    # Raw `recommendation` JSONB from the producer; shape varies per rule, so
    # typed Any — change_summary() parses the rule families it understands.
    recommendation: Any = None


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
    if amount == int(amount):
        return f"${int(amount)}"  # "$820", not "$820.00" — cents are noise
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


def legacy_attachment(
    color: str,
    fallback: str,
    text: str = "",
    actions: Optional[List[Dict[str, Any]]] = None,
) -> Dict[str, Any]:
    """A LEGACY attachment: color stripe + mrkdwn text + URL buttons, and
    nothing else. Slack stamps an "Added by {app}" byline on attachments that
    carry Block Kit ``blocks`` (standalone row) or a ``footer``/``ts`` field
    (appended to the footer row) — verified live — so none of those keys may
    ever appear here. Identity/facts ride the last line of ``text``."""
    attachment: Dict[str, Any] = {"color": color, "fallback": fallback, "mrkdwn_in": ["text"]}
    if text:
        attachment["text"] = text
    if actions:
        # Docs require callback_id on attachments carrying buttons; link
        # buttons never post an interaction payload, so this is inert.
        attachment["callback_id"] = "nb_link_actions"
        attachment["actions"] = actions
    return attachment


def link_button(text: str, url: str, style: str = "") -> Dict[str, Any]:
    """Legacy-attachment URL button — ``text`` is a plain string here, not a
    Block Kit text object."""
    button: Dict[str, Any] = {"type": "button", "text": text, "url": url}
    if style:
        button["style"] = style
    return button


def neutral_footer_attachment(
    text: str = "",
    actions: Optional[List[Dict[str, Any]]] = None,
    fallback: str = "More",
) -> Dict[str, Any]:
    return legacy_attachment(STRIPE_NEUTRAL, fallback, text=text, actions=actions)


def header_block(text: str) -> Dict[str, Any]:
    """Top-level headline with real visual weight (Slack renders `header`
    blocks large). Plain text only — keep formulations asterisk-free."""
    return {"type": "header", "text": {"type": "plain_text", "text": text[:150]}}


def item_facts_line(severity: str, account_name: str) -> str:
    """The trailing facts line of an item: ``High priority · Acct: prod``.
    No finops score, no band word — the stripe carries urgency, and the
    label:value colon keeps the account name readable."""
    bits = []
    if severity:
        bits.append(f"{severity} priority")
    if account_name:
        bits.append(f"Acct: {account_name}")
    return " · ".join(bits)


def accounts_scope(account_names: List[str]) -> str:
    """Headline scope: name the account when there is exactly one
    ("in prod-aws"), count them otherwise ("across 3 accounts")."""
    names = [n for n in account_names if n]
    if len(account_names) == 1 and names:
        return f"in {names[0]}"
    return f"across {accounts_phrase(len(account_names))}"


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


def cost_headline(total_savings: float, scope: str) -> str:
    return f"You can cut {format_savings(total_savings)}/mo {scope}"


def _format_cores(value: float) -> str:
    if value >= 1:
        trimmed = f"{value:.1f}".rstrip("0").rstrip(".")
        return f"{trimmed} core" if trimmed == "1" else f"{trimmed} cores"
    return f"{int(round(value * 1000))}m"


def _format_memory_bytes(value: float) -> str:
    mib = value / (1024 * 1024)
    if mib >= 1024:
        trimmed = f"{mib / 1024:.1f}".rstrip("0").rstrip(".")
        return f"{trimmed}Gi"
    return f"{int(round(mib))}Mi"


def _pod_rightsizing_summary(recommendation: Dict[str, Any]) -> str:
    """recommendation = {container: [{resource, allocated{request}, recommended{request}}]}
    with CPU in cores and memory in bytes (same shape the exports parse)."""
    per_container: List[Tuple[str, str]] = []
    for container, resources in recommendation.items():
        if not isinstance(resources, list):
            continue
        bits = []
        for res in resources:
            if not isinstance(res, dict):
                continue
            current = (res.get("allocated") or {}).get("request")
            proposed = (res.get("recommended") or {}).get("request")
            if current is None or proposed is None or current == proposed:
                continue
            if res.get("resource") == "cpu":
                bits.append(f"CPU *{_format_cores(current)} → {_format_cores(proposed)}*")
            elif res.get("resource") == "memory":
                bits.append(f"Memory *{_format_memory_bytes(current)} → {_format_memory_bytes(proposed)}*")
        if bits:
            per_container.append((container, " · ".join(bits)))
    if not per_container:
        return ""
    container, line = per_container[0]
    if len(per_container) == 1:
        return line
    return f"{container}: {line} · +{len(per_container) - 1} more containers"


def _replica_rightsizing_summary(recommendation: Dict[str, Any]) -> str:
    """Replica payloads nest under a ``recommendation`` key with either
    allocated_replica/recommended_replica scalars or allocated/recommended
    time-series (last actual, first proposed)."""
    data = recommendation.get("recommendation")
    if not isinstance(data, dict):
        data = recommendation
    current = data.get("allocated_replica")
    if current is None:
        series = data.get("allocated")
        if isinstance(series, list) and series and isinstance(series[-1], dict):
            current = series[-1].get("replicas")
    proposed = data.get("recommended_replica")
    if proposed is None:
        series = data.get("recommended")
        if isinstance(series, list) and series and isinstance(series[0], dict):
            proposed = series[0].get("replicas")
    if current is None or proposed is None or current == proposed:
        return ""
    return f"Replicas *{int(current)} → {int(proposed)}*"


def change_summary(rule_name: str, recommendation: Any) -> str:
    """One mrkdwn line stating the change itself (current → recommended) so a
    rightsizing item carries its numbers, not just a rule name. Unknown rule
    shapes and malformed producer JSON yield '' — never a failed digest."""
    if not isinstance(recommendation, dict):
        return ""
    try:
        if rule_name == "pod_right_sizing":
            return _pod_rightsizing_summary(recommendation)
        if rule_name == "replica_right_sizing":
            return _replica_rightsizing_summary(recommendation)
    except (TypeError, ValueError, AttributeError, KeyError, IndexError):
        return ""
    return ""


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
    """One colored legacy attachment per item, in priority order: what & money
    first, then the concrete change, then severity/account in the small plain
    footer. `color` is a hex string or a callable(rec) -> hex."""
    attachments: List[Dict[str, Any]] = []
    for account_name, rec in ranked[:MAX_ALERT_ITEMS]:
        title = f"{format_rule_name(rec.rule_name)} — {short_resource_name(rec.resource_name)}"
        lines = [f"*{title}*"]
        if rec.estimated_savings > 0:
            lines.append(
                f"Save *{format_savings(rec.estimated_savings)}/mo*{format_waste_clause(rec.wasted_since_detected)}"
            )
        change = change_summary(rec.rule_name, rec.recommendation)
        if change:
            lines.append(change)

        facts = item_facts_line(rec.severity, account_name)
        if facts:
            lines.append(facts)

        actions = None
        if rec.cta_url:
            actions = [link_button("Details", _absolute_url(rec.cta_url, base_url), style="primary")]

        stripe = color(rec) if callable(color) else color
        attachments.append(legacy_attachment(stripe, title, text="\n".join(lines), actions=actions))
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
        scope = accounts_scope([acc.account_name for acc in params.recommendations_by_account.values()])
        blocks.append(header_block(cost_headline(params.total_recoverable_savings, scope)))
        branding_line = f"{settings.urls.branding_name} daily brief"
        if params.digest_date:
            branding_line += f" · {params.digest_date}"
        blocks.append({"type": "context", "elements": [{"type": "mrkdwn", "text": branding_line}]})
    else:
        blocks.append(header_block(f"{settings.urls.branding_name} {params.title}"))
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
    remaining = len(ranked) - MAX_ALERT_ITEMS
    attachments.append(
        neutral_footer_attachment(
            text=f"_+{remaining} more in the dashboard_" if remaining > 0 else "",
            actions=[link_button("View All Recommendations", footer_url, style="primary")],
            fallback="View all recommendations",
        )
    )

    return {
        "text": f"{params.title} - {format_savings(params.total_recoverable_savings)}/mo recoverable",
        "blocks": blocks[:50],
        "attachments": attachments[:20],
        "unfurl_links": False,
    }
