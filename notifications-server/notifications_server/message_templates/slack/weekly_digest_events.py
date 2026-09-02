"""Slack renderer for the weekly digest.

Produced by llm-server (api/event_analysis_digest_notify.go) once a tenant-week
digest is generated. The message is a summary, never the review: a channel has
no RBAC and Slack caps a message at 50 blocks, while a busy tenant-week runs to
30 findings across every account in the tenant. Scoreboard, the three findings
worth opening first, and a link to the tab that holds the rest.

Also the home of the shared params model and helpers — the Teams, Google Chat
and Discord renderers import from here, matching how the FinOps nudge digest is
laid out.
"""

from typing import Any, Dict, List

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    header_block,
    legacy_attachment,
    neutral_footer_attachment,
)


class ReviewFinding(BaseModel):
    # Label is the model's human name for the class; aggregation_key is the
    # identity people actually grep for, so both travel and both are shown.
    label: str = ""
    aggregation_key: str = ""
    account_name: str = ""
    cloud_account_id: str = ""
    headline: str = ""
    priority: str = ""
    cause: str = ""
    env: str = ""
    events: int = 0
    carried_over_weeks: int = 0


class WeeklyDigestParams(BaseModel):
    title: str = "Weekly Digest"
    period_start: str = ""
    period_end: str = ""
    period_label: str = ""

    events_analysed: int = 0
    events_complete: int = 0
    completion_pct: int = 0
    failed_events: int = 0
    failure_classes: int = 0
    services: int = 0
    new_incidents: int = 0
    recurrences: int = 0
    recurrence_pct: int = 0
    noise_pct: int = 0
    p1_pct: int = 0

    lede: str = ""
    top_findings: List[ReviewFinding] = []
    total_findings: int = 0
    more_findings: int = 0
    accounts_named: int = 0
    base_url: str = ""
    digest_url: str = ""
    organization_id: str = ""


def get_weekly_digest_events_message_params(**params) -> WeeklyDigestParams:
    raw = params.get("top_findings") or []
    params["top_findings"] = [ReviewFinding(**f) if isinstance(f, dict) else f for f in raw]
    return WeeklyDigestParams(**params)


# Stripe palette matches the nudge digest so the two weekly messages read as one
# product rather than two. Retained for the channels that still render cards.
STRIPE_P1 = "#C93A36"
STRIPE_P2 = "#D97A2B"
STRIPE_OTHER = "#94A3B8"

_P1 = {"P1", "HIGH", "CRITICAL"}
_P2 = {"P2", "MEDIUM"}


def priority_stripe(priority: str) -> str:
    p = (priority or "").strip().upper()
    if p in _P1:
        return STRIPE_P1
    if p in _P2:
        return STRIPE_P2
    return STRIPE_OTHER


_NUMBER_WORDS = {1: "One", 2: "Two", 3: "Three", 4: "Four", 5: "Five", 6: "Six", 7: "Seven", 8: "Eight", 9: "Nine"}


def _count_word(n: int) -> str:
    return _NUMBER_WORDS.get(n, str(n))


def digest_verdict(params: WeeklyDigestParams) -> str:
    """One sentence of judgement, above the figures.

    A count states inventory ("25 failure classes"); this states what the week
    means. Ranked by what a reader can act on: a class nobody has fixed in weeks
    outranks a loud one-off, and drowning in noise outranks either, because it
    invalidates everything else on the page.
    """
    stale = [f for f in params.top_findings if f.carried_over_weeks >= 2]
    if stale:
        n = len(stale)
        verb = "has" if n == 1 else "have"
        noun = "class" if n == 1 else "classes"
        return f"{_count_word(n)} {noun} {verb} now run 2+ weeks."
    if params.noise_pct >= 40:
        return f"{params.noise_pct}% of this week's events were noise."
    if params.total_findings:
        noun = "finding" if params.total_findings == 1 else "findings"
        return f"{params.total_findings} {noun} across the tenant this week."
    return "Nothing broke this week."


def accounts_clause(count: int) -> str:
    if count <= 1:
        return ""
    return f" across {count} accounts"


def digest_headline(params: WeeklyDigestParams) -> str:
    """Lead with how much broke.

    Counts failure classes, NOT failed_events: that field is pipeline health
    ("did the analysis run and finish"), so using it here reported "1 failure
    this week" for a week carrying 27 failure classes and 19 findings — a busy
    week reading as a quiet one. Pipeline health still shows, as "47 of 58
    analysed" in the scoreboard, which is where it belongs.

    Falls back to the finding count when the class counter is absent, and says
    so plainly when nothing broke at all.
    """
    count = params.failure_classes or params.total_findings
    if count == 0:
        return f"No failures this week{accounts_clause(params.accounts_named)}"
    # "Alert class" is the term the Digests tab uses for the same thing; Slack
    # inventing its own word for it just makes the two disagree.
    noun = "alert class" if count == 1 else "alert classes"
    return f"{count} {noun}{accounts_clause(params.accounts_named)}"


def scoreboard_pairs(params: WeeklyDigestParams) -> List[tuple]:
    """The week's figures as (label, value), shared by all four channels.

    Analysed pairs the completed count with the total rather than showing a bare
    percentage: "95 of 108" tells a reader what was missed, "88%" does not.

    Two rules, both driven by how Slack actually renders a section's `fields`:
    two per row. Zero-valued figures are dropped — "Noise 0% of events" costs a
    row to say nothing — but dropping one leaves an odd count, and an odd count
    renders as a lone orphan hanging under a full row. So the list is padded
    back to even from reserve figures that do have values. Only if nothing is
    left to pad with does an odd count survive.

    One list rather than a copy per renderer: four hand-maintained copies of the
    same figures is four chances for them to disagree about the same week.
    """
    classes = str(params.failure_classes)
    if params.services:
        classes += f" · {params.services} services"

    # Headline figures first, then reserves used only to even out the grid.
    primary = [
        ("Analysed", f"{params.events_complete} of {params.events_analysed}", True),
        ("Alert classes", classes, bool(params.failure_classes)),
        ("Recurring", f"{params.recurrences} ({params.recurrence_pct}%)", bool(params.recurrences)),
        ("Noise", f"{params.noise_pct}% of events", bool(params.noise_pct)),
    ]
    reserves = [
        ("New this week", str(params.new_incidents), bool(params.new_incidents)),
        ("P1 share", f"{params.p1_pct}% of events", bool(params.p1_pct)),
    ]

    pairs = [(label, value) for label, value, populated in primary if populated]
    for label, value, populated in reserves:
        if len(pairs) % 2 == 0:
            break
        if populated:
            pairs.append((label, value))
    return pairs


def scoreboard_fields(params: WeeklyDigestParams) -> List[Dict[str, str]]:
    """Slack two-column field set built from the shared figures."""
    return [{"type": "mrkdwn", "text": f"*{label}*\n{value}"} for label, value in scoreboard_pairs(params)]


def scoreboard_blocks(params: WeeklyDigestParams) -> List[Dict[str, Any]]:
    """The figures as one section per row of two.

    All four fields in a single section makes Slack lay them out with no row
    boundary, so the pairs run together — "95 of 108Classes". A section per pair
    gives each row its own block, and blocks are spaced apart.
    """
    fields = scoreboard_fields(params)
    return [{"type": "section", "fields": fields[i : i + 2]} for i in range(0, len(fields), 2)]


def finding_title(f: ReviewFinding) -> str:
    """Human label first, falling back to the key when the model gave none."""
    return f.label or f.aggregation_key or "Unnamed failure class"


def finding_facts_line(f: ReviewFinding) -> str:
    """Trailing facts as one line, for the channels without a field layout.

    Account is never dropped — at tenant scope the same aggregation key can be
    two unrelated incidents in two accounts, so a finding without its account
    is ambiguous rather than merely terse.
    """
    bits: List[str] = []
    if f.priority:
        bits.append(f"{f.priority}")
    if f.account_name:
        bits.append(f"Acct: {f.account_name}")
    if f.env and f.env.lower() != "unknown":
        bits.append(f.env)
    if f.carried_over_weeks > 0:
        week_word = "week" if f.carried_over_weeks == 1 else "weeks"
        bits.append(f"{f.carried_over_weeks} {week_word} running")
    if f.events:
        bits.append(f"{f.events} events")
    return " · ".join(bits)


def build_finding_attachments(params: WeeklyDigestParams) -> List[Dict[str, Any]]:
    """One coloured attachment per finding, using legacy keys only.

    Blocks nested inside an attachment would give a real heading/body/caption
    hierarchy, but Slack stamps an "Added by {app}" row on any attachment
    carrying them — one per card. Every other template in this service uses
    top-level blocks plus legacy attachments for exactly that reason, and none
    of them are stamped.

    So hierarchy is built from what legacy text allows: a bold title, a plain
    description, an italic caption, and `fields` for the facts grid. Less
    separation than blocks would give, and no byline.
    """
    attachments: List[Dict[str, Any]] = []
    for f in params.top_findings:
        lines = [f"*{finding_title(f)}*"]
        if f.headline:
            lines.append(f.headline)
        facts = finding_context_line(f)
        if facts:
            lines.append(f"_{facts}_")

        attachments.append(
            legacy_attachment(
                priority_stripe(f.priority),
                finding_title(f),
                text="\n".join(lines),
            )
        )
    return attachments


def finding_context_line(f: ReviewFinding) -> str:
    """The caption under a finding: where, how long, how loud.

    Priority is deliberately absent — the coloured dot already says it, and
    repeating it in the caption spends the line twice. Account always shows: at
    tenant scope the same class in two accounts is two unrelated incidents.
    """
    bits: List[str] = []
    if f.account_name:
        account = f.account_name
        # "unknown" is an inference gap, not an environment worth printing.
        if f.env and f.env.lower() != "unknown":
            account += f" · {f.env}"
        bits.append(account)
    if f.carried_over_weeks > 0:
        week_word = "wk" if f.carried_over_weeks == 1 else "wks"
        bits.append(f"{f.carried_over_weeks} {week_word} running")
    else:
        bits.append("new this week")
    if f.events:
        bits.append(f"{f.events} events")
    return "  ·  ".join(bits)


def digest_link(params: WeeklyDigestParams) -> str:
    """Deep link to the Digests tab.

    b-Cortex is a modal with no route of its own, so the app reads a ?bcortex=
    param on load (NubiBrainNav) and opens the tab. It has to land on /home:
    the root route redirects there without carrying its query, which drops the
    param. Producer-supplied when set; rebuilt from the base URL otherwise so
    an older payload still links usefully.

    A producer-supplied URL is only trusted when it is absolute. Chat clients
    cannot resolve a relative path — they render it as literal text or drop the
    link entirely — so a producer with no base URL configured falls through to
    the fallback below instead of shipping a dead link.
    """
    if params.digest_url.startswith(("http://", "https://")):
        return params.digest_url
    base = (params.base_url or public_ip()).rstrip("/")
    return f"{base}/home?bcortex=digests"


def get_weekly_digest_events_message_template(params: WeeklyDigestParams) -> Dict[str, Any]:
    """Top-level blocks for the summary, coloured attachments for the findings.

    Attachments alone flatten everything to one text size; blocks alone lose the
    colour band. Blocks nested inside a coloured attachment give both, at the
    cost of Slack's per-attachment byline.
    """
    blocks: List[Dict[str, Any]] = [
        {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": f"*{settings.urls.branding_name} weekly digest*"
                + (f"  ·  {params.period_label}" if params.period_label else ""),
            },
        }
    ]

    # Verdict and scope share one line. As separate blocks they rendered as two
    # stacked fragments that read as a single run-on sentence — "14 findings"
    # butted straight against "Three classes have now run 2+ weeks."
    scope: List[str] = []
    if params.accounts_named:
        scope.append(f"{params.accounts_named} account" + ("" if params.accounts_named == 1 else "s"))
    if params.total_findings:
        scope.append(f"{params.total_findings} findings")

    headline = f"*{digest_verdict(params)}*"
    if scope:
        headline += "  ·  " + ", ".join(scope)
    blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": headline}})
    blocks.append({"type": "divider"})
    blocks.extend(scoreboard_blocks(params))

    # The model's lede runs to several sentences on real data; below the figures
    # and in small text it supports them instead of burying them.
    if params.lede:
        blocks.append({"type": "context", "elements": [{"type": "mrkdwn", "text": params.lede[:2900]}]})

    attachments = build_finding_attachments(params)
    if attachments:
        blocks.append({"type": "divider"})
        shown = len(params.top_findings)
        heading = "*What broke*"
        if params.total_findings > shown:
            heading += f"  ·  {shown} of {params.total_findings}"
        blocks.append({"type": "section", "text": {"type": "mrkdwn", "text": heading}})

    # Footer rides a neutral attachment so it lands below the findings. Legacy
    # keys only — adding `blocks` here would earn another byline for one row.
    #
    # No link button on Slack: the complete review is attached to this message
    # as a PDF, so a button offering the same thing behind a login is noise. The
    # other three channels keep the link (see their own renderers) because the
    # attachment is Slack-only. If the PDF cannot be produced, the attach step
    # posts the link into the thread instead, so the review is never unreachable.
    footer_bits = []
    if params.more_findings > 0:
        footer_bits.append(f"_+{params.more_findings} more in the full review_")
    footer_bits.append("_Full review attached below._")
    attachments.append(neutral_footer_attachment(text="  ·  ".join(footer_bits), fallback="Full review attached"))

    return {
        # Notification-tray fallback for clients that can't render blocks.
        "text": f"{params.title} · {digest_headline(params)}",
        "blocks": blocks[:50],
        "attachments": attachments[:20],
        "unfurl_links": False,
    }
