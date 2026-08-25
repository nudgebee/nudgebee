"""AI cost daily report template: one consolidated Slack message per tenant,
all accounts in ONE native Slack table block (sorted MTD-cost desc, capped at
MAX_ACCOUNTS_SHOWN rows), a "+N more" footer link beyond that cap, and minor
cost drivers (under MIN_DRIVER_CONTRIBUTION_PCT of the account's total)
dropped from the driver cells."""

import json
from typing import Any, Dict, List, Tuple

from notifications_server.message_templates.slack.ai_cost_daily_report import (
    ACCOUNT_TABLE_HEADERS,
    MAX_ACCOUNTS_SHOWN,
    MAX_DRIVERS_PER_CELL,
    MIN_DRIVER_CONTRIBUTION_PCT,
    TOP_MODELS_TABLE_HEADERS,
    TOP_SOURCES_TABLE_HEADERS,
    AiCostDriver,
    TopModelRow,
    TopSourceRow,
    _driver_cell,
    _top_models_reply_blocks,
    _top_models_table_block,
    _top_sources_reply_blocks,
    _top_sources_table_block,
    format_delta,
    format_usd,
    get_ai_cost_account_report_message_params,
    get_ai_cost_account_report_message_template,
    trend_arrow,
)


def _account(name: str, mtd: float, daily: float = 0.0, prev_month: float = 0.0, **drivers) -> Dict[str, Any]:
    return {
        "account_id": f"acc-{name}",
        "account_name": name,
        "daily_cost_usd": daily,
        "mtd_cost_usd": mtd,
        "prev_month_cost_usd": prev_month,
        "avg_daily_this_month_usd": mtd / 10,
        "avg_daily_prev_month_usd": prev_month / 30,
        "pct_delta_avg_daily": 0,
        **drivers,
    }


def _table_blocks(msg: Dict[str, Any]) -> List[Dict[str, Any]]:
    return [b for b in msg["blocks"] if b.get("type") == "table"]


def _cell_text(cell: Dict[str, Any]) -> str:
    if cell.get("type") == "raw_text":
        return cell.get("text", "")
    if cell.get("type") == "rich_text":
        return "".join(sub.get("text", "") for el in cell.get("elements", []) for sub in el.get("elements", []))
    return ""


def _account_rows(table_block: Dict[str, Any]) -> List[Dict[str, str]]:
    """Header row -> column names; every row after that -> one dict per account."""
    headers = [_cell_text(c) for c in table_block["rows"][0]]
    return [dict(zip(headers, (_cell_text(c) for c in row))) for row in table_block["rows"][1:]]


def _action_elements(msg: Dict[str, Any]) -> List[Dict[str, Any]]:
    actions_block = next(b for b in msg["blocks"] if b.get("type") == "actions")
    return actions_block["elements"]


def _action_button_url(msg: Dict[str, Any]) -> str:
    return _action_elements(msg)[0]["url"]


def _button_by_action_id(msg: Dict[str, Any], action_id: str) -> Dict[str, Any]:
    return next(e for e in _action_elements(msg) if e.get("action_id") == action_id)


def _button_rows(button: Dict[str, Any]) -> List[List[Any]]:
    """Decode a "show table" button's value — the compact rows the click
    handler (services/actions_common.py) will build the table from, without
    ever calling llm-server again."""
    payload = json.loads(button["value"])
    return payload["body"]["action_params"]["rows"]


def _context_texts(msg: Dict[str, Any]) -> List[str]:
    return [el["text"] for b in msg["blocks"] if b.get("type") == "context" for el in b.get("elements", [])]


def test_accounts_sorted_by_mtd_cost_desc():
    params = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07",
        accounts=[_account("Low", mtd=5.0), _account("High", mtd=500.0), _account("Mid", mtd=50.0)],
    )
    msg = get_ai_cost_account_report_message_template(params)
    tables = _table_blocks(msg)
    assert len(tables) == 1, "all accounts belong in one consolidated table, not one per account"
    rows = _account_rows(tables[0])
    assert rows[0]["Account"] == "High"
    assert rows[1]["Account"] == "Mid"
    assert rows[2]["Account"] == "Low"


def test_driver_cell_only_shows_when_drivers_present():
    with_drivers = _account("Acme", mtd=100.0, top_mtd_by_model=[{"key": "gpt-4o", "cost_usd": 80.0}])
    without_drivers = _account("Beta", mtd=20.0)
    params = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07", accounts=[with_drivers, without_drivers]
    )
    msg = get_ai_cost_account_report_message_template(params)
    rows = _account_rows(_table_blocks(msg)[0])
    assert rows[0]["Top MTD by model"] == "gpt-4o $80.00"
    assert rows[1]["Top MTD by model"] == "—"


def _drivers(*pairs: Tuple[str, float]) -> List[AiCostDriver]:
    return [AiCostDriver(key=key, cost_usd=cost) for key, cost in pairs]


def test_driver_cell_keeps_top_driver_and_drops_minor_contributors():
    # MAX_DRIVERS_PER_CELL caps the main table's cells at the top driver only
    # — even a source breakdown with several contributors clearing the 10%
    # bar shows just the top one here.
    assert MAX_DRIVERS_PER_CELL == 1
    model_drivers = _drivers(
        ("gemini-3.1-pro-preview", 24.47),
        ("gemini-3-flash-preview", 0.08),
        ("gemini-2.5-flash", 0.01),
        ("gemini-2.5-flash-lite", 0.01),
    )
    source_drivers = _drivers(
        ("Investigation", 13.54),
        ("Automation", 6.60),
        ("pr_lifecycle", 3.15),
        ("UserInvestigation", 0.66),
        ("InstantNotification", 0.62),
    )
    assert _driver_cell(model_drivers, total=24.57) == "gemini-3.1-pro-preview $24.47"
    assert _driver_cell(source_drivers, total=24.57) == "Investigation $13.54"


def test_driver_cell_shows_top_driver_even_below_ten_percent():
    # The top driver is unconditional — even a single driver under the 10%
    # bar (a wide, flat spend spread across many models/sources) still shows,
    # so the cell is never empty when driver data actually exists.
    drivers = _drivers(("tiny-model", 50.0))  # 5% of a 1000.0 total
    assert _driver_cell(drivers, total=1000.0) == "tiny-model $50.00"


def test_driver_cell_caps_at_max_drivers_per_cell():
    drivers = _drivers(*[(f"m{i}", 100.0 - i) for i in range(8)])  # all well above 10% of total
    result = _driver_cell(drivers, total=100.0)
    assert result.count("\n") == MAX_DRIVERS_PER_CELL - 1  # MAX_DRIVERS_PER_CELL entries joined by newlines
    assert "m0" in result
    assert "m1" not in result


def test_driver_cell_zero_total_shows_only_top_driver():
    # total <= 0 shouldn't divide-by-zero; MIN_DRIVER_CONTRIBUTION_PCT is
    # meaningless with no baseline, so only the unconditional top driver shows.
    assert MIN_DRIVER_CONTRIBUTION_PCT == 10
    drivers = _drivers(("only-model", 5.0), ("second", 4.0))
    assert _driver_cell(drivers, total=0.0) == "only-model $5.00"


def test_footer_shows_remaining_count_beyond_cap():
    accounts = [_account(f"Acct{i}", mtd=float(100 - i)) for i in range(MAX_ACCOUNTS_SHOWN + 3)]
    params = get_ai_cost_account_report_message_params(reference_date="2026-08-07", accounts=accounts)
    msg = get_ai_cost_account_report_message_template(params)
    # Still one table, just capped at MAX_ACCOUNTS_SHOWN rows.
    tables = _table_blocks(msg)
    assert len(tables) == 1
    assert len(_account_rows(tables[0])) == MAX_ACCOUNTS_SHOWN
    assert any("+3 more account(s)" in t for t in _context_texts(msg))


def test_footer_link_pins_dashboard_to_this_digests_reference_date():
    # Without asOf, the dashboard's Cost Report tab defaults to "today" (live) and
    # can show different numbers than what the digest just reported (today's
    # cost is still partial). The link must carry the exact date so clicking
    # through shows the same numbers regardless of when it's clicked.
    params = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07", accounts=[_account("Acme", mtd=100.0)]
    )
    msg = get_ai_cost_account_report_message_template(params)
    footer_url = _action_button_url(msg)
    assert "asOf=2026-08-07" in footer_url
    assert footer_url.endswith("#cost-analyser/cost-report")


def test_action_buttons_follow_accounts_table_view_link_first():
    # Top models/sources aren't rendered as inline tables at all anymore —
    # they're behind buttons in the same actions block as "View in Cost
    # Analyser", which comes right after the (sole) accounts table.
    params = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07",
        accounts=[_account("Acme", mtd=100.0)],
        top_models=[_top_model("gpt-4o", 100.0, 50, 3.5, 9.2)],
        top_sources=[_top_source("Investigation", 80.0, 40, 2.5, 6.0)],
    )
    msg = get_ai_cost_account_report_message_template(params)
    assert len(_table_blocks(msg)) == 1, "only the accounts table renders inline"
    types = [b.get("type") for b in msg["blocks"]]
    assert types.index("table") < types.index("actions")
    texts = [e["text"]["text"] for e in _action_elements(msg)]
    assert texts == ["View in Cost Analyser", "Top Models", "Top Sources"]


def test_empty_accounts_still_renders_a_valid_message():
    params = get_ai_cost_account_report_message_params(reference_date="2026-08-07", accounts=[])
    msg = get_ai_cost_account_report_message_template(params)
    assert msg["blocks"]
    assert not _table_blocks(msg), "no accounts means no table at all, not an empty one"
    # The "View in Cost Analyser" button still renders even with no accounts.
    assert _action_button_url(msg).endswith("#cost-analyser/cost-report")
    assert "unfurl_links" in msg and msg["unfurl_links"] is False


def test_format_usd_abbreviates_like_the_dashboard():
    # Matches app/src/components/llm/cost-analyser/format.ts's fmtCost exactly,
    # so a cost above $1,000 reads the same on both surfaces.
    assert format_usd(999.99) == "$999.99"
    assert format_usd(1234.56) == "$1.23K"
    assert format_usd(1_000_000) == "$1.00M"
    assert format_usd(1_234_567.89) == "$1.23M"


def test_format_usd_signs_negative_amounts_before_the_dollar():
    # f"${amount:...}" on a negative amount reads "$-1.23K", which looks like
    # a typo — the sign belongs before the "$", not between it and the digits.
    assert format_usd(-1234.56) == "-$1.23K"
    assert format_usd(-999.99) == "-$999.99"
    assert format_usd(-1_000_000) == "-$1.00M"


def test_format_delta_avoids_negative_zero():
    # Python's :.0f format preserves the sign on a negative value that rounds
    # to zero (f"{-0.4:.0f}" == "-0"), so signing off the raw pct before
    # rounding renders a misleading "-0%" for what's really a flat trend.
    assert format_delta(-0.4) == "0%"
    assert format_delta(0.4) == "0%"
    assert format_delta(0) == "0%"
    assert format_delta(24.6) == "+25%"
    assert format_delta(-24.6) == "-25%"


def test_trend_arrow_bands():
    assert trend_arrow(25) == "▲"
    assert trend_arrow(-10) == "▼"
    assert trend_arrow(0) == "→"


def test_account_row_has_every_stat_and_correct_column_order():
    params = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07",
        accounts=[_account("Acme", mtd=100.0, daily=10.0, prev_month=310.0)],
    )
    msg = get_ai_cost_account_report_message_template(params)
    table = _table_blocks(msg)[0]
    headers = [_cell_text(c) for c in table["rows"][0]]
    assert headers == ACCOUNT_TABLE_HEADERS
    row = _account_rows(table)[0]
    assert row["Daily"] == "$10.00"
    # Prev-month -> MTD is one combined value, not two separate columns, and
    # "MTD" alone would misdescribe the prev-month half of the value.
    assert row["Cost (prev mo → MTD)"] == "$310.00 → $100.00"
    assert row["Account"] == "Acme"


def test_avg_daily_prev_to_cur_includes_trend_arrow_and_delta():
    params = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07",
        accounts=[_account("Acme", mtd=100.0, prev_month=300.0)],  # avg/day: prev=10.0, this=10.0
    )
    msg = get_ai_cost_account_report_message_template(params)
    row = _account_rows(_table_blocks(msg)[0])[0]
    assert row["Avg/day (prev → cur)"] == "$10.00 → $10.00 (→ 0%)"


def _top_model(model: str, mtd_cost: float, calls: int, p95: float, p99: float) -> Dict[str, Any]:
    return {
        "model": model,
        "mtd_cost_usd": mtd_cost,
        "call_count": calls,
        "p95_cost_per_call_usd": p95,
        "p99_cost_per_call_usd": p99,
    }


def test_top_models_button_carries_rows_and_absent_when_no_models():
    params = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07",
        accounts=[_account("Acme", mtd=100.0)],
        top_models=[_top_model("gpt-4o", 100.0, 50, 3.5, 9.2)],
    )
    msg = get_ai_cost_account_report_message_template(params)
    assert len(_table_blocks(msg)) == 1, "top models is a button, not an inline table"

    button = _button_by_action_id(msg, "ai_cost_top_models")
    payload = json.loads(button["value"])
    assert payload["body"]["action_name"] == "ai_cost_show_top_models"
    assert _button_rows(button) == [["gpt-4o", 100.0, 50, 3.5, 9.2]]

    without_models = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07", accounts=[_account("Acme", mtd=100.0)]
    )
    msg_without = get_ai_cost_account_report_message_template(without_models)
    assert not any(e.get("action_id") == "ai_cost_top_models" for e in _action_elements(msg_without))


def test_top_models_button_preserves_go_query_order_and_cap():
    # llm-server's SQL already sorts by MTD cost desc and caps at
    # topModelsLimit — this template must not re-sort or re-slice the rows
    # it hands to the button.
    models = [_top_model(f"model-{i}", 100.0 - i, 10, 1.0, 2.0) for i in range(10)]
    params = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07", accounts=[_account("Acme", mtd=100.0)], top_models=models
    )
    msg = get_ai_cost_account_report_message_template(params)
    rows = _button_rows(_button_by_action_id(msg, "ai_cost_top_models"))
    assert len(rows) == 10
    assert [r[0] for r in rows] == [f"model-{i}" for i in range(10)]


def test_top_models_table_block_has_every_column():
    # The table itself (built by the click handler from the button's rows,
    # see services/actions_common.py) still has every stat, unfiltered.
    block = _top_models_table_block(
        [
            TopModelRow(
                model="gpt-4o", mtd_cost_usd=100.0, call_count=50, p95_cost_per_call_usd=3.5, p99_cost_per_call_usd=9.2
            )
        ]
    )
    headers = [_cell_text(c) for c in block["rows"][0]]
    assert headers == TOP_MODELS_TABLE_HEADERS
    row = _account_rows(block)[0]
    assert row["Model"] == "gpt-4o"
    assert row["MTD cost"] == "$100.00"
    assert row["# of calls"] == "50"
    assert row["p95 cost/call"] == "$3.50"
    assert row["p99 cost/call"] == "$9.20"


def test_top_models_reply_blocks_pairs_a_heading_with_the_table():
    # The click handler posts these as a single threaded reply (services/
    # actions_common.py) — heading and table must travel together so the
    # reply is self-explanatory without the button's label.
    blocks = _top_models_reply_blocks(
        [
            TopModelRow(
                model="gpt-4o", mtd_cost_usd=100.0, call_count=50, p95_cost_per_call_usd=3.5, p99_cost_per_call_usd=9.2
            )
        ]
    )
    assert len(blocks) == 2
    assert blocks[0] == {"type": "section", "text": {"type": "mrkdwn", "text": "*Top models (tenant-wide MTD)*"}}
    assert blocks[1]["type"] == "table"


def _top_source(source: str, mtd_cost: float, calls: int, p95: float, p99: float) -> Dict[str, Any]:
    return {
        "source": source,
        "mtd_cost_usd": mtd_cost,
        "call_count": calls,
        "p95_cost_per_call_usd": p95,
        "p99_cost_per_call_usd": p99,
    }


def test_top_sources_button_carries_rows_and_absent_when_no_sources():
    params = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07",
        accounts=[_account("Acme", mtd=100.0)],
        top_sources=[_top_source("Investigation", 80.0, 40, 2.5, 6.0)],
    )
    msg = get_ai_cost_account_report_message_template(params)
    assert len(_table_blocks(msg)) == 1, "top sources is a button, not an inline table"

    button = _button_by_action_id(msg, "ai_cost_top_sources")
    payload = json.loads(button["value"])
    assert payload["body"]["action_name"] == "ai_cost_show_top_sources"
    assert _button_rows(button) == [["Investigation", 80.0, 40, 2.5, 6.0]]

    without_sources = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07", accounts=[_account("Acme", mtd=100.0)]
    )
    msg_without = get_ai_cost_account_report_message_template(without_sources)
    assert not any(e.get("action_id") == "ai_cost_top_sources" for e in _action_elements(msg_without))


def test_top_sources_button_preserves_go_query_order_and_cap():
    # llm-server's SQL already sorts by MTD cost desc and caps at
    # topSourcesLimit (5) — this template must not re-sort or re-slice the
    # rows it hands to the button.
    sources = [_top_source(f"source-{i}", 100.0 - i, 10, 1.0, 2.0) for i in range(5)]
    params = get_ai_cost_account_report_message_params(
        reference_date="2026-08-07", accounts=[_account("Acme", mtd=100.0)], top_sources=sources
    )
    msg = get_ai_cost_account_report_message_template(params)
    rows = _button_rows(_button_by_action_id(msg, "ai_cost_top_sources"))
    assert len(rows) == 5
    assert [r[0] for r in rows] == [f"source-{i}" for i in range(5)]


def test_top_sources_table_block_has_every_column():
    block = _top_sources_table_block(
        [
            TopSourceRow(
                source="Investigation",
                mtd_cost_usd=80.0,
                call_count=40,
                p95_cost_per_call_usd=2.5,
                p99_cost_per_call_usd=6.0,
            )
        ]
    )
    headers = [_cell_text(c) for c in block["rows"][0]]
    assert headers == TOP_SOURCES_TABLE_HEADERS
    row = _account_rows(block)[0]
    assert row["Source"] == "Investigation"
    assert row["MTD cost"] == "$80.00"
    assert row["# of calls"] == "40"
    assert row["p95 cost/call"] == "$2.50"
    assert row["p99 cost/call"] == "$6.00"


def test_top_sources_reply_blocks_pairs_a_heading_with_the_table():
    blocks = _top_sources_reply_blocks(
        [
            TopSourceRow(
                source="Investigation",
                mtd_cost_usd=80.0,
                call_count=40,
                p95_cost_per_call_usd=2.5,
                p99_cost_per_call_usd=6.0,
            )
        ]
    )
    assert len(blocks) == 2
    assert blocks[0] == {"type": "section", "text": {"type": "mrkdwn", "text": "*Top sources (tenant-wide MTD)*"}}
    assert blocks[1]["type"] == "table"
