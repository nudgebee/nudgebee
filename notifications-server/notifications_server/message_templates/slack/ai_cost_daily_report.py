import json
from typing import Dict, Any, List

from pydantic import BaseModel

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.slack.recommendation_nudge_digest import header_block

# ai_cost_daily_report.py renders the consolidated per-account AI cost digest
# (llm-server's core.RunAiCostDailyDigest, published via publishAiCostAccountReport)
# — daily + month-to-date cost, previous-month comparison, and top model/source
# drivers, one row per account in a single tenant-wide message. Field names
# mirror the Go payload's json tags exactly (core.AiCostAccountRow /
# core.AiCostDriver) so no field remapping is needed on this side.
#
# All accounts render as ONE native Slack `table` block, built directly as
# raw Slack JSON rather than via markdown_table.py's GFM-pipe-table route —
# driver cells hold one driver per line (a literal "\n" in a raw_text cell),
# and a GFM pipe table's one-row-per-source-line format can't represent an
# embedded newline inside a cell. A color-striped legacy_attachment can't
# carry Block Kit `blocks` either way (Slack stamps an "Added by {app}"
# byline on any attachment that does, verified live). No color/emoji trend
# indicator — the "Δ avg/day" column's arrow + signed percentage already
# carries that.
#
# The top-models/top-sources tables are NOT rendered inline — they're behind
# "Top Models"/"Top Sources" buttons that post the table as a threaded reply
# on click (services/actions_common.py's handle_show_ai_cost_top_models /
# handle_show_ai_cost_top_sources). The button's `value` carries the already-
# computed rows directly (compact arrays, well under Slack's 2000-char value
# cap for the row counts here) so the click handler never re-queries
# llm-server — everything it needs was already in this digest.


class AiCostDriver(BaseModel):
    key: str
    cost_usd: float = 0


class AiCostAccountRow(BaseModel):
    account_id: str
    account_name: str
    daily_cost_usd: float = 0
    mtd_cost_usd: float = 0
    prev_month_cost_usd: float = 0
    avg_daily_this_month_usd: float = 0
    avg_daily_prev_month_usd: float = 0
    pct_delta_avg_daily: float = 0
    top_daily_by_model: List[AiCostDriver] = []
    top_daily_by_source: List[AiCostDriver] = []
    top_mtd_by_model: List[AiCostDriver] = []
    top_mtd_by_source: List[AiCostDriver] = []


class TopModelRow(BaseModel):
    model: str
    mtd_cost_usd: float = 0
    call_count: int = 0
    p95_cost_per_call_usd: float = 0
    p99_cost_per_call_usd: float = 0


class TopSourceRow(BaseModel):
    source: str
    mtd_cost_usd: float = 0
    call_count: int = 0
    p95_cost_per_call_usd: float = 0
    p99_cost_per_call_usd: float = 0


class AiCostAccountReportParams(BaseModel):
    tenant_id: str = ""
    reference_date: str = ""
    accounts: List[AiCostAccountRow] = []
    # Tenant-wide (not per-account), top topModelsLimit/topSourcesLimit
    # (llm-server) models/sources by MTD cost — already sorted desc and
    # capped by the Go query.
    top_models: List[TopModelRow] = []
    top_sources: List[TopSourceRow] = []


def get_ai_cost_account_report_message_params(**params) -> AiCostAccountReportParams:
    return AiCostAccountReportParams(**params)


def format_usd(amount: float) -> str:
    # K/M abbreviation matches the dashboard's fmtCost (app/src/components/llm/
    # cost-analyser/format.ts) exactly, so a cost above $1,000 reads the same
    # on both surfaces instead of Slack showing the full comma-separated figure
    # while the dashboard abbreviates it.
    # Sign goes before the "$", not after it — f"${amount:...}" on a negative
    # amount would otherwise render "$-1.23K" instead of "-$1.23K".
    sign = "-" if amount < 0 else ""
    v = abs(amount)
    if v >= 1_000_000:
        return f"{sign}${v / 1_000_000:.2f}M"
    if v >= 1_000:
        return f"{sign}${v / 1_000:.2f}K"
    return f"{sign}${v:,.2f}"


def format_delta(pct: float) -> str:
    # Round first, then sign off the rounded value — signing off the raw pct
    # lets Python's negative-zero formatting through (f"{-0.4:.0f}" == "-0"),
    # showing "-0%" for a small negative that rounds to flat.
    rounded = round(pct)
    if rounded == 0:
        return "0%"
    sign = "+" if rounded > 0 else ""
    return f"{sign}{rounded:.0f}%"


def trend_arrow(pct: float) -> str:
    if pct > 0.5:
        return "▲"
    if pct < -0.5:
        return "▼"
    return "→"


# The digest shows at most this many accounts inline; the rest is a "+N more"
# link to the dashboard — mirrors MAX_ALERT_ITEMS in recommendation_nudge_digest.
MAX_ACCOUNTS_SHOWN = 5

# A driver's minimum share of the account's total to be worth a reader's
# attention — see _driver_cell.
MIN_DRIVER_CONTRIBUTION_PCT = 10

# Max drivers shown per account cell (including the top driver) in the main
# accounts table. Was a literal 5; kept the rest of _driver_cell's slice/
# filter logic as-is and swapped in this constant so the count can be dialed
# independently of MIN_DRIVER_CONTRIBUTION_PCT.
MAX_DRIVERS_PER_CELL = 1


def _driver_cell(drivers: List[AiCostDriver], total: float, separator: str = "\n") -> str:
    # The top driver always shows (it's the headline "what drove this cost"
    # answer even if nothing else clears the bar), but drivers beyond
    # MAX_DRIVERS_PER_CELL are only worth showing if they're a real
    # contributor — anything under MIN_DRIVER_CONTRIBUTION_PCT of the
    # account's total is noise that just pads the cell without changing the
    # story. One driver per line (not a single run-on line) so a
    # multi-driver cell stays scannable in a table.
    if not drivers:
        return "—"
    top, rest = drivers[0], drivers[1:MAX_DRIVERS_PER_CELL]
    selected = [top]
    if total > 0:
        selected += [d for d in rest if (d.cost_usd / total * 100) >= MIN_DRIVER_CONTRIBUTION_PCT]
    return separator.join(f"{d.key} {format_usd(d.cost_usd)}" for d in selected)


def _prev_to_cur(prev: float, cur: float) -> str:
    return f"{format_usd(prev)} → {format_usd(cur)}"


# Column headers for the consolidated accounts table, in display order.
ACCOUNT_TABLE_HEADERS = [
    "Account",
    "Daily",
    "Avg/day (prev → cur)",
    # Not "MTD" alone — this column spans prev-month AND current MTD totals,
    # so a bare "MTD" header would misdescribe the prev-month half of the value.
    "Cost (prev mo → MTD)",
    "Top MTD by model",
    "Top MTD by source",
]


def _table_cell(text: str) -> Dict[str, Any]:
    return {"type": "raw_text", "text": text}


def _avg_daily_cell(row: AiCostAccountRow) -> str:
    # The prev->cur avg/day trend and its arrow/delta share one column — the
    # arrow+delta is exactly what "prev -> cur" changed by, so it reads as
    # self-explanatory here without needing a "(Δ ...)" label in the header.
    trend = f"{trend_arrow(row.pct_delta_avg_daily)} {format_delta(row.pct_delta_avg_daily)}"
    return f"{_prev_to_cur(row.avg_daily_prev_month_usd, row.avg_daily_this_month_usd)} ({trend})"


def _account_table_row(row: AiCostAccountRow) -> List[Dict[str, Any]]:
    return [
        _table_cell(row.account_name),
        _table_cell(format_usd(row.daily_cost_usd)),
        _table_cell(_avg_daily_cell(row)),
        _table_cell(_prev_to_cur(row.prev_month_cost_usd, row.mtd_cost_usd)),
        _table_cell(_driver_cell(row.top_mtd_by_model, row.mtd_cost_usd)),
        _table_cell(_driver_cell(row.top_mtd_by_source, row.mtd_cost_usd)),
    ]


def _accounts_table_block(accounts: List[AiCostAccountRow]) -> Dict[str, Any]:
    """The consolidated accounts table as a raw Slack `table` block — every
    stat the old per-account fields-based card showed, none dropped, just
    consolidated into one table instead of one table per account. Slack
    renders the first row as the header automatically."""
    rows = [[_table_cell(h) for h in ACCOUNT_TABLE_HEADERS]] + [_account_table_row(row) for row in accounts]
    return {"type": "table", "rows": rows}


# Column headers for the tenant-wide top-models table, in display order.
TOP_MODELS_TABLE_HEADERS = ["Model", "MTD cost", "# of calls", "p95 cost/call", "p99 cost/call"]


def _top_model_table_row(m: TopModelRow) -> List[Dict[str, Any]]:
    return [
        _table_cell(m.model),
        _table_cell(format_usd(m.mtd_cost_usd)),
        _table_cell(str(m.call_count)),
        _table_cell(format_usd(m.p95_cost_per_call_usd)),
        _table_cell(format_usd(m.p99_cost_per_call_usd)),
    ]


def _top_models_table_block(models: List[TopModelRow]) -> Dict[str, Any]:
    """Tenant-wide top-models table — already sorted by MTD cost desc and
    capped by the Go query, so no re-sorting/slicing needed here."""
    rows = [[_table_cell(h) for h in TOP_MODELS_TABLE_HEADERS]] + [_top_model_table_row(m) for m in models]
    return {"type": "table", "rows": rows}


def _table_heading_block(text: str) -> Dict[str, Any]:
    return {"type": "section", "text": {"type": "mrkdwn", "text": f"*{text}*"}}


def _top_models_reply_blocks(models: List[TopModelRow]) -> List[Dict[str, Any]]:
    """Heading + table in one list so the click handler posts both as a single
    threaded reply — keeps the two trackable together instead of the table
    showing up with nothing above it to say what it is."""
    return [_table_heading_block("Top models (tenant-wide MTD)"), _top_models_table_block(models)]


# Column headers for the tenant-wide top-sources table, in display order.
TOP_SOURCES_TABLE_HEADERS = ["Source", "MTD cost", "# of calls", "p95 cost/call", "p99 cost/call"]


def _top_source_table_row(s: TopSourceRow) -> List[Dict[str, Any]]:
    return [
        _table_cell(s.source),
        _table_cell(format_usd(s.mtd_cost_usd)),
        _table_cell(str(s.call_count)),
        _table_cell(format_usd(s.p95_cost_per_call_usd)),
        _table_cell(format_usd(s.p99_cost_per_call_usd)),
    ]


def _top_sources_table_block(sources: List[TopSourceRow]) -> Dict[str, Any]:
    """Tenant-wide top-sources table — already sorted by MTD cost desc and
    capped by the Go query, so no re-sorting/slicing needed here."""
    rows = [[_table_cell(h) for h in TOP_SOURCES_TABLE_HEADERS]] + [_top_source_table_row(s) for s in sources]
    return {"type": "table", "rows": rows}


def _top_sources_reply_blocks(sources: List[TopSourceRow]) -> List[Dict[str, Any]]:
    """Mirrors _top_models_reply_blocks for the source breakdown."""
    return [_table_heading_block("Top sources (tenant-wide MTD)"), _top_sources_table_block(sources)]


def _compact_model_rows(models: List[TopModelRow]) -> List[List[Any]]:
    return [[m.model, m.mtd_cost_usd, m.call_count, m.p95_cost_per_call_usd, m.p99_cost_per_call_usd] for m in models]


def _compact_source_rows(sources: List[TopSourceRow]) -> List[List[Any]]:
    return [[s.source, s.mtd_cost_usd, s.call_count, s.p95_cost_per_call_usd, s.p99_cost_per_call_usd] for s in sources]


def _show_table_button(text: str, action_id: str, action_name: str, rows: List[List[Any]]) -> Dict[str, Any]:
    # `value`'s envelope matches generic.py's _build_action_value convention —
    # services/actions_common.py's generic action_name dispatch decodes it
    # with no lookup beyond this payload, so the handler never calls llm-server.
    return {
        "type": "button",
        "text": {"type": "plain_text", "text": text},
        "action_id": action_id,
        "value": json.dumps({"body": {"action_name": action_name, "action_params": {"rows": rows}}}),
    }


def get_ai_cost_account_report_message_template(params: AiCostAccountReportParams) -> Dict[str, Any]:
    base_url = public_ip()
    total_mtd = sum(a.mtd_cost_usd for a in params.accounts)
    accounts = sorted(params.accounts, key=lambda a: a.mtd_cost_usd, reverse=True)

    title = f"{settings.urls.branding_name} AI Cost Report — {params.reference_date}"
    blocks: List[Dict[str, Any]] = [
        header_block(f"💰 {title}"),
        {
            "type": "context",
            "elements": [
                {"type": "mrkdwn", "text": f"*{format_usd(total_mtd)}* MTD across *{len(accounts)}* account(s)"}
            ],
        },
    ]

    shown = accounts[:MAX_ACCOUNTS_SHOWN]
    if shown:
        blocks.append(_accounts_table_block(shown))

    # "View in Cost Analyser" sits between the two tables (not after both, and
    # not in a legacy attachment — Slack always renders attachments after every
    # block regardless of where they're defined, so a native `actions` block is
    # the only way to place a button anywhere but the very end of the message).
    remaining = len(accounts) - MAX_ACCOUNTS_SHOWN
    if remaining > 0:
        blocks.append(
            {
                "type": "context",
                "elements": [{"type": "mrkdwn", "text": f"_+{remaining} more account(s) in the dashboard_"}],
            }
        )
    # The /cost-report sub-fragment lands directly on the Cost Report tab (CostAnalyser.tsx
    # reads it as a deep link) instead of the LLM Analyser's default Overview tab.
    # asOf pins the dashboard to this digest's exact reference date — without it,
    # the Cost Report tab defaults to "today" (live), which can show different numbers
    # than what the digest just reported (today's cost is still partial; the digest
    # always reports on the last fully-completed day). This keeps the two consistent
    # regardless of when the link is actually clicked.
    footer_url = f"{base_url}/optimise?utm=slack-digest&asOf={params.reference_date}#cost-analyser/cost-report"
    action_elements: List[Dict[str, Any]] = [
        {
            "type": "button",
            "text": {"type": "plain_text", "text": "View in Cost Analyser"},
            "url": footer_url,
            "style": "primary",
        }
    ]
    # Top models/sources aren't rendered inline — clicking these posts the
    # table as a threaded reply (see module docstring), using the rows
    # already computed here rather than showing them unconditionally.
    if params.top_models:
        action_elements.append(
            _show_table_button(
                "Top Models", "ai_cost_top_models", "ai_cost_show_top_models", _compact_model_rows(params.top_models)
            )
        )
    if params.top_sources:
        action_elements.append(
            _show_table_button(
                "Top Sources",
                "ai_cost_top_sources",
                "ai_cost_show_top_sources",
                _compact_source_rows(params.top_sources),
            )
        )
    blocks.append({"type": "actions", "elements": action_elements})

    return {
        "text": f"{title} — {format_usd(total_mtd)} MTD",
        "blocks": blocks[:50],
        "unfurl_links": False,
    }
