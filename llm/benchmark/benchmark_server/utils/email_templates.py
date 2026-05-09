"""
Beautiful HTML email templates for benchmark reports.

Uses table-based layouts with inline CSS for maximum email client compatibility.
Branding matches NudgeBee product emails (DM Serif Display + DM Sans, #facf39 primary).
"""

from datetime import datetime
from typing import Optional


# -- Brand constants (aligned with app/src/utils/colors.ts) --
_BRAND_NAME = "NudgeBee"
_PRIMARY_COLOR = "#2563EB"  # colors.primary
_NUDGEBEE_MAIN = "#FACF39"  # colors.nudgebeeMain (accent/yellow)
_DARK_PRIMARY = "#1F61A4"  # colors.darkPrimary
_DARK_BG = "#10264c"  # footer bg (from invite.html)
_BODY_BG = "#F0F2F5"
_CARD_BG = "#FFFFFF"
_TEXT_DARK = "#000000"  # colors.black
_TEXT_SECONDARY = "#374151"  # colors.secondary.default / text.secondary
_TEXT_MID = "#3D3D3D"
_TEXT_LIGHT = "#737373"  # colors.tertiary
_TEXT_LIGHTEST = "#BBBBBB"
_SUCCESS = "#16A34A"  # colors.success
_ERROR = "#DC2626"  # colors.error
_INFO = "#3B82F6"  # colors.info
_LOGO_URL = (
    "https://s3.amazonaws.com/nudgebee-documents-v2/images/nudgebee-logo-dark-bg.png"
)
_FONT_SERIF = "'DM Serif Display', Georgia, serif"
_FONT_SANS = "'DM Sans', -apple-system, Arial, sans-serif"
_GOOGLE_FONTS = (
    '<link rel="preconnect" href="https://fonts.googleapis.com" />'
    '<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin="anonymous" />'
    '<link href="https://fonts.googleapis.com/css2?family=DM+Serif+Display:ital@0;1'
    '&amp;family=DM+Sans:wght@300;400;500;600&amp;display=swap" rel="stylesheet" />'
)


def _color_for_score(score: float) -> str:
    """Return a color based on score (0-100), using app color system."""
    if score >= 80:
        return _SUCCESS  # #16A34A (colors.success)
    elif score >= 60:
        return "#EAB308"  # colors.yellow
    else:
        return _ERROR  # #DC2626 (colors.error)


def _format_number(value, fallback="N/A") -> str:
    if value is None or value == "N/A":
        return fallback
    if isinstance(value, float):
        return f"{value:.2f}"
    return str(value)


def _gauge_bar(label: str, value: float, suffix: str = "%") -> str:
    """Create an inline progress bar for a metric."""
    color = _color_for_score(value)
    width = max(2, min(100, value))
    return f"""
    <tr>
        <td style="padding: 8px 0;">
            <table width="100%" cellpadding="0" cellspacing="0" border="0">
                <tr>
                    <td style="font-size: 13px; color: #64748b; padding-bottom: 4px; font-family: {_FONT_SANS};">
                        {label}
                    </td>
                    <td align="right" style="font-size: 15px; font-weight: 700; color: {color}; padding-bottom: 4px; font-family: {_FONT_SANS};">
                        {_format_number(value)}{suffix}
                    </td>
                </tr>
                <tr>
                    <td colspan="2">
                        <table width="100%" cellpadding="0" cellspacing="0" border="0" style="border-radius: 6px; overflow: hidden;">
                            <tr>
                                <td style="background-color: #f1f5f9; border-radius: 6px; height: 8px;">
                                    <table cellpadding="0" cellspacing="0" border="0" style="border-radius: 6px;" width="{width}%">
                                        <tr>
                                            <td style="background-color: {color}; height: 8px; border-radius: 6px;">&nbsp;</td>
                                        </tr>
                                    </table>
                                </td>
                            </tr>
                        </table>
                    </td>
                </tr>
            </table>
        </td>
    </tr>"""


def _stat_card(label: str, value: str) -> str:
    """Create a stat card for a single KPI."""
    return f"""
    <td width="33%" style="padding: 6px;">
        <table width="100%" cellpadding="0" cellspacing="0" border="0" style="background-color: {_CARD_BG}; border: 1px solid #e2e8f0; border-radius: 12px;">
            <tr>
                <td style="padding: 16px; text-align: center;">
                    <div style="font-size: 11px; color: {_TEXT_SECONDARY}; text-transform: uppercase; letter-spacing: 0.5px; font-weight: 700; font-family: {_FONT_SANS}; margin-bottom: 6px;">
                        {label}
                    </div>
                    <div style="font-size: 22px; font-weight: 800; color: {_TEXT_SECONDARY}; font-family: {_FONT_SANS}; line-height: 1.2;">
                        {value}
                    </div>
                </td>
            </tr>
        </table>
    </td>"""


def _tag_row(tag: str, data: dict, idx: int) -> str:
    """Create a table row for a tag breakdown."""
    bg = _CARD_BG if idx % 2 == 0 else "#f8fafc"
    acc = data.get("overall_accuracy", 0)
    color = _color_for_score(acc)
    planner_val = data.get("planner_relevancy", 0)
    planner_color = _color_for_score(planner_val)
    return f"""
    <tr style="background-color: {bg};">
        <td style="padding: 8px 12px; font-weight: 600; color: {_TEXT_DARK}; font-size: 13px; font-family: {_FONT_SANS}; border-bottom: 1px solid #f1f5f9;">
            {tag}
        </td>
        <td align="center" style="padding: 8px 6px; font-size: 12px; color: #64748b; font-family: {_FONT_SANS}; border-bottom: 1px solid #f1f5f9;">
            {data.get('count', 0)}
        </td>
        <td align="center" style="padding: 8px 6px; border-bottom: 1px solid #f1f5f9;">
            <span style="background-color: {color}15; color: {color}; font-weight: 700; padding: 2px 8px; border-radius: 10px; font-size: 12px; font-family: {_FONT_SANS};">
                {_format_number(acc)}%
            </span>
        </td>
        <td align="center" style="padding: 8px 6px; border-bottom: 1px solid #f1f5f9;">
            <span style="background-color: {planner_color}15; color: {planner_color}; font-weight: 600; padding: 2px 8px; border-radius: 10px; font-size: 12px; font-family: {_FONT_SANS};">
                {_format_number(planner_val)}%
            </span>
        </td>
        <td align="center" style="padding: 8px 6px; color: #475569; font-size: 12px; font-family: {_FONT_SANS}; border-bottom: 1px solid #f1f5f9;">
            {_format_number(data.get('avg_duration_seconds', 0))}s
        </td>
        <td align="center" style="padding: 8px 6px; color: #475569; font-size: 12px; font-family: {_FONT_SANS}; border-bottom: 1px solid #f1f5f9;">
            {_format_number(data.get('avg_tool_calls', 0))}
        </td>
        <td align="center" style="padding: 8px 6px; color: #475569; font-size: 12px; font-family: {_FONT_SANS}; border-bottom: 1px solid #f1f5f9;">
            ${_format_number(data.get('avg_cost_usd', 0))}
        </td>
    </tr>"""


def build_benchmark_email(
    report_data: dict,
    display_date: Optional[str] = None,
) -> str:
    """
    Build a branded HTML email from benchmark report data.

    Args:
        report_data: The full benchmark report dict (metadata, summary, by_tag, details).
        display_date: Optional formatted date string for the header.

    Returns:
        Complete HTML string for the email body.
    """
    metadata = report_data.get("metadata", {})
    summary = report_data.get("summary", {})
    latency = summary.get("latency", {})
    tool_calls = summary.get("tool_calls", {})
    cost = summary.get("cost", {})
    tokens = summary.get("tokens", {})
    by_tag = report_data.get("by_tag", {})

    agent = metadata.get("agent", "unknown").upper()
    run_name = metadata.get("run_name", "")
    run_label = run_name or agent
    total_queries = metadata.get("total_queries", "N/A")
    overall_acc = summary.get("overall_accuracy", 0)

    if display_date is None:
        ts = metadata.get("timestamp", "")
        if ts:
            try:
                dt = datetime.fromisoformat(ts)
                display_date = dt.strftime("%B %d, %Y at %H:%M UTC")
            except (ValueError, TypeError):
                display_date = ts
        else:
            display_date = datetime.now().strftime("%B %d, %Y at %H:%M UTC")

    model_names = metadata.get("model_names", [])
    model_str = ", ".join(model_names) if model_names else "N/A"

    # Build accuracy gauge bars
    gauge_bars = ""
    gauge_bars += _gauge_bar("Answer Similarity", summary.get("answer_similarity", 0))
    gauge_bars += _gauge_bar("Answer Relevancy", summary.get("answer_relevancy", 0))

    # Build efficiency gauge bars (separate section)
    efficiency_bars = ""
    efficiency_bars += _gauge_bar(
        "Planner Relevancy", summary.get("planner_relevancy", 0)
    )
    efficiency_bars += _gauge_bar(
        "Tool Success Rate", tool_calls.get("success_rate", 0)
    )
    efficiency_bars += _gauge_bar("Cache Hit Rate", tokens.get("avg_cache_hit_rate", 0))

    # Build stat cards
    stat_cards_row1 = ""
    stat_cards_row1 += _stat_card(
        "Avg Latency",
        f"{_format_number(latency.get('avg_seconds', 0))}s",
    )
    stat_cards_row1 += _stat_card(
        "P95 Latency",
        f"{_format_number(latency.get('p95_seconds', 0))}s",
    )
    stat_cards_row1 += _stat_card(
        "P99 Latency",
        f"{_format_number(latency.get('p99_seconds', 0))}s",
    )

    stat_cards_row1b = ""
    stat_cards_row1b += _stat_card(
        "Max Latency",
        f"{_format_number(latency.get('max_seconds', 0))}s",
    )
    stat_cards_row1b += _stat_card(
        "Total Time",
        f"{_format_number(latency.get('total_seconds', 0))}s",
    )
    stat_cards_row1b += _stat_card(
        "Total Queries",
        str(total_queries),
    )

    stat_cards_row2 = ""
    stat_cards_row2 += _stat_card(
        "Total Cost",
        f"${_format_number(cost.get('total_usd', 0))}",
    )
    stat_cards_row2 += _stat_card(
        "Total Tokens",
        (
            f"{tokens.get('total', 'N/A'):,}"
            if isinstance(tokens.get("total"), (int, float))
            else str(tokens.get("total", "N/A"))
        ),
    )
    stat_cards_row2 += _stat_card(
        "Tool Calls",
        str(tool_calls.get("total", "N/A")),
    )

    # Build tag breakdown table
    tag_section = ""
    if by_tag:
        tag_rows = ""
        for idx, (tag, data) in enumerate(sorted(by_tag.items())):
            tag_rows += _tag_row(tag, data, idx)

        tag_section = f"""
        <!-- Tag Breakdown -->
        <tr>
            <td style="padding: 0 24px 24px;">
                <table width="100%" cellpadding="0" cellspacing="0" border="0" style="background-color: {_CARD_BG}; border-radius: 12px; border: 1px solid #e2e8f0; overflow: hidden;">
                    <tr>
                        <td style="padding: 20px 20px 4px; font-size: 16px; font-weight: 700; color: {_TEXT_DARK}; font-family: {_FONT_SANS};">
                            &#128202; Performance by Category <span style="font-size: 12px; font-weight: 400; color: #94a3b8;">(tests may appear in multiple tags)</span>
                        </td>
                    </tr>
                    <tr>
                        <td style="padding: 8px 20px 20px;">
                            <table width="100%" cellpadding="0" cellspacing="0" border="0" style="border-radius: 8px; overflow: hidden; border: 1px solid #e2e8f0;">
                                <tr style="background-color: #f8fafc;">
                                    <td width="22%" style="padding: 10px 12px; font-size: 11px; font-weight: 700; color: #64748b; text-transform: uppercase; letter-spacing: 0.5px; font-family: {_FONT_SANS}; border-bottom: 2px solid #e2e8f0;">Category</td>
                                    <td width="6%" align="center" style="padding: 10px 6px; font-size: 11px; font-weight: 700; color: #64748b; text-transform: uppercase; letter-spacing: 0.5px; font-family: {_FONT_SANS}; border-bottom: 2px solid #e2e8f0;">Count</td>
                                    <td width="16%" align="center" style="padding: 10px 6px; font-size: 11px; font-weight: 700; color: #64748b; text-transform: uppercase; letter-spacing: 0.5px; font-family: {_FONT_SANS}; border-bottom: 2px solid #e2e8f0;">Accuracy</td>
                                    <td width="14%" align="center" style="padding: 10px 6px; font-size: 11px; font-weight: 700; color: #818CF8; text-transform: uppercase; letter-spacing: 0.5px; font-family: {_FONT_SANS}; border-bottom: 2px solid #e2e8f0;">Planner</td>
                                    <td width="14%" align="center" style="padding: 10px 6px; font-size: 11px; font-weight: 700; color: #64748b; text-transform: uppercase; letter-spacing: 0.5px; font-family: {_FONT_SANS}; border-bottom: 2px solid #e2e8f0;">Avg Time</td>
                                    <td width="14%" align="center" style="padding: 10px 6px; font-size: 11px; font-weight: 700; color: #64748b; text-transform: uppercase; letter-spacing: 0.5px; font-family: {_FONT_SANS}; border-bottom: 2px solid #e2e8f0;">Avg Tools</td>
                                    <td width="14%" align="center" style="padding: 10px 6px; font-size: 11px; font-weight: 700; color: #64748b; text-transform: uppercase; letter-spacing: 0.5px; font-family: {_FONT_SANS}; border-bottom: 2px solid #e2e8f0;">Avg Cost</td>
                                </tr>
                                {tag_rows}
                            </table>
                        </td>
                    </tr>
                </table>
            </td>
        </tr>"""

    # Compute cost efficiency
    acc_per_dollar = cost.get("accuracy_per_dollar", 0)
    cost_per_query = cost.get("avg_per_query_usd", 0)

    html = f"""<!DOCTYPE html>
<html lang="en" xmlns="http://www.w3.org/1999/xhtml">
<head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta http-equiv="X-UA-Compatible" content="IE=edge" />
    <meta name="x-apple-disable-message-reformatting" />
    <title>{run_label} Benchmark Report</title>
    {_GOOGLE_FONTS}
    <style>
        body, table, td, a {{ -webkit-text-size-adjust:100%; -ms-text-size-adjust:100%; }}
        table, td {{ mso-table-lspace:0pt; mso-table-rspace:0pt; }}
        @media only screen and (max-width:660px) {{
            .email-card {{ width:100% !important; padding:36px 20px 32px !important; }}
            .footer-wrap {{ width:100% !important; }}
        }}
    </style>
</head>
<body style="margin:0; padding:0; background-color:{_BODY_BG}; font-family:{_FONT_SANS}; -webkit-font-smoothing:antialiased;">

    <!-- Inbox preview text (hidden) -->
    <div style="display:none; max-height:0; overflow:hidden; mso-hide:all; font-size:1px; color:{_BODY_BG}; line-height:1px;">
        {run_label} Benchmark: {_format_number(overall_acc)}% accuracy across {total_queries} queries
    </div>

    <!-- Outer wrapper -->
    <table style="border-collapse:collapse; border-spacing:0; border:0; width:100%; background-color:{_BODY_BG};">
        <tr>
            <th style="font-weight:normal; text-align:center; padding:40px 16px;">

                <!-- Email card -->
                <table class="email-card"
                    style="border-collapse:collapse; border-spacing:0; border:0; width:660px; margin:0 auto; background-color:{_CARD_BG}; border-radius:4px; box-shadow:0 1px 3px rgba(0,0,0,0.06),0 4px 20px rgba(0,0,0,0.04);">

                    <!-- HEADER with brand bar -->
                    <tr>
                        <th style="font-weight:normal; text-align:left; padding:0;">
                            <table style="border-collapse:collapse; border-spacing:0; border:0; width:100%; background-color:{_DARK_BG}; border-radius:4px 4px 0 0;">
                                <tr>
                                    <th style="font-weight:normal; text-align:left; padding:24px 40px;">
                                        <img alt="{_BRAND_NAME}" src="{_LOGO_URL}" style="height:28px; width:auto; display:inline-block; vertical-align:middle;" />
                                    </th>
                                </tr>
                            </table>
                        </th>
                    </tr>

                    <!-- Accent bar -->
                    <tr>
                        <td style="background-color:{_NUDGEBEE_MAIN}; height:4px; font-size:0; line-height:0;">&nbsp;</td>
                    </tr>

                    <!-- HERO: Score -->
                    <tr>
                        <th style="font-weight:normal; text-align:center; padding:36px 40px 28px;">
                            <p style="font-family:{_FONT_SANS}; font-size:12px; color:{_TEXT_SECONDARY}; text-transform:uppercase; letter-spacing:1.5px; margin:0 0 8px;">
                                {run_label} Benchmark
                            </p>
                            <p style="font-family:{_FONT_SERIF}; font-size:56px; color:{_TEXT_DARK}; font-weight:400; line-height:1; margin:0; letter-spacing:-1px;">
                                {_format_number(overall_acc)}<span style="font-size:28px; color:#94a3b8;">%</span>
                            </p>
                            <p style="font-family:{_FONT_SANS}; font-size:14px; color:{_TEXT_MID}; margin:8px 0 0; font-weight:300;">
                                Overall Accuracy &middot; {total_queries} queries
                            </p>
                            <p style="font-family:{_FONT_SANS}; font-size:12px; color:{_TEXT_LIGHT}; margin:10px 0 0; font-weight:300;">
                                {display_date}
                            </p>
                        </th>
                    </tr>

                    <!-- Model info -->
                    <tr>
                        <td style="padding:0 40px;">
                            <table style="border-collapse:collapse; border-spacing:0; border:0; width:100%; background-color:#f8fafc; border-radius:8px; border:1px solid #F0F0F0;">
                                <tr>
                                    <td style="padding:10px 16px; text-align:center;">
                                        <span style="font-family:{_FONT_SANS}; font-size:12px; color:#64748b;">
                                            Model: <strong style="color:{_TEXT_DARK};">{model_str}</strong>
                                        </span>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>

                    <!-- Divider -->
                    <tr>
                        <td style="padding:24px 40px 0;">
                            <table style="border-collapse:collapse; border-spacing:0; border:0; width:100%;">
                                <tr><td style="border-top:1px solid #F0F0F0; font-size:0; line-height:0;">&nbsp;</td></tr>
                            </table>
                        </td>
                    </tr>

                    <!-- ACCURACY -->
                    <tr>
                        <td style="padding:20px 40px 12px;">
                            <table width="100%" cellpadding="0" cellspacing="0" border="0" style="background-color:{_CARD_BG}; border-radius:12px; border:1px solid #e2e8f0;">
                                <tr>
                                    <td style="padding:20px 20px 4px; font-family:{_FONT_SERIF}; font-size:18px; color:{_TEXT_DARK}; letter-spacing:-0.3px;">
                                        Accuracy <span style="font-size:13px; color:#94a3b8; font-weight:400;">(Overall = avg of Similarity + Relevancy)</span>
                                    </td>
                                </tr>
                                <tr>
                                    <td style="padding:4px 20px 20px;">
                                        <table width="100%" cellpadding="0" cellspacing="0" border="0">
                                            {gauge_bars}
                                        </table>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>

                    <!-- EFFICIENCY -->
                    <tr>
                        <td style="padding:4px 40px 12px;">
                            <table width="100%" cellpadding="0" cellspacing="0" border="0" style="background-color:{_CARD_BG}; border-radius:12px; border:1px solid #e2e8f0;">
                                <tr>
                                    <td style="padding:20px 20px 4px; font-family:{_FONT_SERIF}; font-size:18px; color:{_TEXT_DARK}; letter-spacing:-0.3px;">
                                        Efficiency
                                    </td>
                                </tr>
                                <tr>
                                    <td style="padding:4px 20px 20px;">
                                        <table width="100%" cellpadding="0" cellspacing="0" border="0">
                                            {efficiency_bars}
                                        </table>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>

                    <!-- LATENCY -->
                    <tr>
                        <td style="padding:12px 40px;">
                            <p style="font-family:{_FONT_SERIF}; font-size:18px; color:{_TEXT_DARK}; letter-spacing:-0.3px; margin:0 0 10px;">
                                Latency
                            </p>
                            <table width="100%" cellpadding="0" cellspacing="0" border="0">
                                <tr>{stat_cards_row1}</tr>
                                <tr>{stat_cards_row1b}</tr>
                            </table>
                        </td>
                    </tr>

                    <!-- COST & RESOURCES -->
                    <tr>
                        <td style="padding:12px 40px;">
                            <p style="font-family:{_FONT_SERIF}; font-size:18px; color:{_TEXT_DARK}; letter-spacing:-0.3px; margin:0 0 10px;">
                                Cost &amp; Resources
                            </p>
                            <table width="100%" cellpadding="0" cellspacing="0" border="0">
                                <tr>{stat_cards_row2}</tr>
                            </table>
                        </td>
                    </tr>

                    <!-- EFFICIENCY -->
                    <tr>
                        <td style="padding:12px 40px;">
                            <table width="100%" cellpadding="0" cellspacing="0" border="0" style="background-color:#FAFAFA; border-radius:12px; border:1px solid #F0F0F0;">
                                <tr>
                                    <td style="padding:16px 20px;">
                                        <table width="100%" cellpadding="0" cellspacing="0" border="0">
                                            <tr>
                                                <td width="50%">
                                                    <div style="font-family:{_FONT_SANS}; font-size:11px; color:#64748b; text-transform:uppercase; letter-spacing:0.5px;">Accuracy / Dollar</div>
                                                    <div style="font-family:{_FONT_SANS}; font-size:20px; font-weight:800; color:#059669;">{_format_number(acc_per_dollar)}</div>
                                                </td>
                                                <td width="50%" align="right">
                                                    <div style="font-family:{_FONT_SANS}; font-size:11px; color:#64748b; text-transform:uppercase; letter-spacing:0.5px;">Cost / Query</div>
                                                    <div style="font-family:{_FONT_SANS}; font-size:20px; font-weight:800; color:#059669;">${_format_number(cost_per_query)}</div>
                                                </td>
                                            </tr>
                                        </table>
                                    </td>
                                </tr>
                            </table>
                        </td>
                    </tr>

                    <!-- Divider before tags -->
                    <tr>
                        <td style="padding:16px 40px 0;">
                            <table style="border-collapse:collapse; border-spacing:0; border:0; width:100%;">
                                <tr><td style="border-top:1px solid #F0F0F0; font-size:0; line-height:0;">&nbsp;</td></tr>
                            </table>
                        </td>
                    </tr>

                    {tag_section}

                    <!-- FOOTER inside card -->
                    <tr>
                        <td style="padding:20px 40px 32px; text-align:center;">
                            <p style="font-family:{_FONT_SANS}; font-size:12px; color:#94a3b8; line-height:1.6; margin:0;">
                                {f'{run_name} &middot; ' if run_name else ''}Run ID: {metadata.get('run_id', 'N/A')}
                            </p>
                            <p style="font-family:{_FONT_SANS}; font-size:12px; color:{_TEXT_LIGHTEST}; margin:4px 0 0; font-weight:300;">
                                Detailed JSON report is attached to this email.
                            </p>
                        </td>
                    </tr>

                </table>
                <!-- /Email card -->

                <!-- FOOTER -->
                <table class="footer-wrap" style="border-collapse:collapse; border-spacing:0; border:0; width:660px; margin:0 auto;">
                    <tr>
                        <th style="font-weight:normal; text-align:center; padding:22px 0 0;">
                            <p style="font-family:{_FONT_SANS}; font-size:11.5px; color:{_TEXT_LIGHTEST}; line-height:1.7; margin:0; font-weight:300;">
                                &copy; {_BRAND_NAME} Benchmark Suite
                                &nbsp;&middot;&nbsp;
                                <a href="https://nudgebee.com" style="color:#CCCCCC; text-decoration:none;">nudgebee.com</a>
                            </p>
                        </th>
                    </tr>
                </table>

            </th>
        </tr>
    </table>

</body>
</html>"""

    return html
