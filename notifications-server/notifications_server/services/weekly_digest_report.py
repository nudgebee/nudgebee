"""Render a stored weekly digest as a PDF.

The Slack message is a summary — a scoreboard and the three findings worth
opening first. This produces the whole review as an attachable document: every
finding, the patterns, the plan, carry-over and signal hygiene.

The full review is read from the database rather than carried in the
notification envelope. The envelope deliberately holds only the top findings
(channel block limits), so putting the complete review in it would inflate every
message for the benefit of one channel that attaches a file.

Drawn with ReportLab directly rather than converted from HTML. xhtml2pdf would
have let the layout live in a template, but it hard-depends on pyHanko (PDF
digital signing), oscrypto, lxml and an RTL text stack — twelve packages for a
document with no signatures, no SVG and no right-to-left text. oscrypto in
particular is unmaintained and fails to detect OpenSSL 3.x, which this service's
Alpine base would be running. ReportLab is the engine xhtml2pdf used underneath
anyway; going direct costs a hand-built layout and drops ten dependencies.
"""

import html
import logging
from datetime import date
from io import BytesIO
from typing import Any, Dict, List, Optional, Tuple

from reportlab.lib import colors
from reportlab.lib.enums import TA_LEFT
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import ParagraphStyle, getSampleStyleSheet
from reportlab.lib.units import mm
from reportlab.platypus import KeepTogether, Paragraph, SimpleDocTemplate, Spacer, Table, TableStyle
from sqlalchemy import text

from notifications_server.configs.settings import settings

LOG = logging.getLogger(__name__)

# A review that somehow renders enormous would be rejected by the upload API and
# is more likely a bug than a real week. Cheaper to refuse than to retry forever.
MAX_PDF_BYTES = 5 * 1024 * 1024

_P1 = {"P1", "HIGH", "CRITICAL"}
_P2 = {"P2", "MEDIUM"}

INK = colors.HexColor("#1F2933")
MUTED = colors.HexColor("#6B7280")
RULE = colors.HexColor("#D7DCE2")
PANEL = colors.HexColor("#F8FAFC")
BAND_P1 = colors.HexColor("#C93A36")
BAND_P2 = colors.HexColor("#D97A2B")
BAND_OTHER = colors.HexColor("#94A3B8")

# CAST(...) rather than the ::uuid shorthand: SQLAlchemy's text() reads ":" as
# the start of a bind parameter, so "::uuid" makes asyncpg raise
# "syntax error at or near :". The verbose form is the unambiguous one.
_DIGEST_QUERY = text("""
    SELECT metrics, class_summaries, briefing, COALESCE(summary, '') AS summary,
           period_start, period_end
    FROM event_analysis_digest
    WHERE tenant_id = CAST(:tenant_id AS uuid)
      AND period_start = CAST(:period_start AS date)
    """)


async def fetch_digest(session, tenant_id: str, period_start: str) -> Optional[Dict[str, Any]]:
    """Read one stored tenant-week. Returns None when the row is gone.

    A missing row is normal rather than exceptional: the generator's migration
    clears history, and a delivery can be retried after the row it described has
    been superseded.

    period_start arrives from the notification envelope as an ISO string, but
    asyncpg binds a date column as a real date and rejects a str outright — the
    SQL-side CAST never gets a chance to run. Parse before binding.
    """
    try:
        period = date.fromisoformat(str(period_start))
    except ValueError:
        LOG.error("weekly digest: unparseable period_start %r", period_start)
        return None

    result = await session.execute(_DIGEST_QUERY, {"tenant_id": tenant_id, "period_start": period})
    row = result.mappings().first()
    return dict(row) if row else None


def _band(priority: str):
    p = (priority or "").strip().upper()
    if p in _P1:
        return BAND_P1
    if p in _P2:
        return BAND_P2
    return BAND_OTHER


def _finding_meta(f: Dict[str, Any]) -> str:
    """The grey line under a finding's title."""
    bits: List[str] = []
    if f.get("priority"):
        bits.append(str(f["priority"]))
    account = f.get("account_name") or ""
    env = f.get("env") or ""
    if account:
        # "unknown" is an inference gap, not an environment worth printing.
        bits.append(f"{account} · {env}" if env and env.lower() != "unknown" else account)
    weeks = f.get("carried_over_weeks") or 0
    bits.append(f"{weeks} week{'' if weeks == 1 else 's'} running" if weeks else "new this week")
    if f.get("events"):
        bits.append(f"{f['events']} events")
    if f.get("aggregation_key"):
        bits.append(str(f["aggregation_key"]))
    return "  ·  ".join(bits)


def _tiles(metrics: Dict[str, Any]) -> List[Tuple[str, str]]:
    """Four headline figures, zero-valued ones dropped."""
    candidates = [
        ("Analysed", f"{metrics.get('events_complete') or 0} / {metrics.get('events_analysed') or 0}", True),
        ("Alert classes", str(metrics.get("failure_classes") or 0), bool(metrics.get("failure_classes"))),
        (
            "Recurring",
            f"{metrics.get('recurrences') or 0} ({metrics.get('recurrence_pct') or 0}%)",
            bool(metrics.get("recurrences")),
        ),
        ("Noise", f"{metrics.get('noise_pct') or 0}%", bool(metrics.get("noise_pct"))),
        ("Services", str(metrics.get("services") or 0), bool(metrics.get("services"))),
    ]
    return [(label, value) for label, value, populated in candidates if populated][:4]


def _hygiene_rows(briefing: Dict[str, Any]) -> List[Tuple[str, str]]:
    # `_dicts` covers the stored lists; hygiene is the one nested object, and a
    # string or list here would raise on .get and cost the whole attachment.
    hygiene = briefing.get("hygiene")
    if not isinstance(hygiene, dict):
        hygiene = {}
    rows = [
        ("Noise", hygiene.get("noise")),
        ("Pipeline", hygiene.get("pipeline")),
        ("Confidence", hygiene.get("confidence")),
    ]
    return [(label, value) for label, value in rows if value]


def _dicts(value: Any) -> List[Dict[str, Any]]:
    """Keep only the object entries of a stored list.

    The briefing is LLM-written JSONB, so a list of objects occasionally comes
    back as a list of bare strings. Every section below calls `.get` on these
    entries, and the resulting AttributeError costs the whole attachment rather
    than one row — so the shape is enforced once, here, where the raw column
    enters, instead of guarding each call site.
    """
    if not isinstance(value, list):
        return []
    return [item for item in value if isinstance(item, dict)]


def build_context(digest: Dict[str, Any], verdict: str, period_label: str, accounts: int) -> Dict[str, Any]:
    metrics = digest.get("metrics") or {}
    briefing = digest.get("briefing") or {}
    if not isinstance(metrics, dict):
        metrics = {}
    if not isinstance(briefing, dict):
        briefing = {}
    findings = _dicts(digest.get("class_summaries"))

    return {
        "brand": settings.urls.branding_name,
        "period_label": period_label,
        "accounts": accounts,
        "verdict": verdict,
        "lede": briefing.get("what_broke_lede") or digest.get("summary") or "",
        "tiles": _tiles(metrics),
        "findings": [
            {
                "title": f.get("label") or f.get("aggregation_key") or "Unnamed failure class",
                "meta": _finding_meta(f),
                "problem": f.get("problem") or f.get("headline") or "",
                "fix": f.get("fix") or "",
                "band": _band(f.get("priority", "")),
            }
            for f in findings
        ],
        "patterns": _dicts(briefing.get("patterns")),
        "plan": _dicts(briefing.get("plan")),
        "carried_over": _dicts(briefing.get("carried_over")),
        "resolved": _dicts(briefing.get("resolved")),
        "hygiene": _hygiene_rows(briefing),
    }


def _styles() -> Dict[str, ParagraphStyle]:
    base = getSampleStyleSheet()["BodyText"]

    def style(name, **kw):
        return ParagraphStyle(name, parent=base, alignment=TA_LEFT, **kw)

    return {
        "title": style("rrTitle", fontName="Helvetica-Bold", fontSize=17, leading=20, textColor=INK, spaceAfter=1),
        "sub": style("rrSub", fontSize=8.5, leading=11, textColor=MUTED, spaceAfter=8),
        "verdict": style("rrVerdict", fontName="Helvetica-Bold", fontSize=11, leading=14, textColor=INK, spaceAfter=2),
        "lede": style("rrLede", fontSize=9.5, leading=13, textColor=colors.HexColor("#4B5563"), spaceAfter=4),
        "h2": style(
            "rrH2", fontName="Helvetica-Bold", fontSize=9, leading=12, textColor=INK, spaceBefore=14, spaceAfter=4
        ),
        "body": style("rrBody", fontSize=9.5, leading=13, textColor=INK),
        "meta": style("rrMeta", fontSize=8, leading=11, textColor=MUTED, spaceAfter=2),
        "tileValue": style("rrTileValue", fontName="Helvetica-Bold", fontSize=14, leading=16, textColor=INK),
        "tileLabel": style("rrTileLabel", fontSize=7.5, leading=10, textColor=MUTED),
        "cell": style("rrCell", fontSize=9, leading=12, textColor=INK),
        "cellMuted": style("rrCellMuted", fontSize=9, leading=12, textColor=MUTED),
        "th": style("rrTh", fontName="Helvetica-Bold", fontSize=7.5, leading=10, textColor=MUTED),
    }


def _rule(width: float):
    """A hairline under a section heading."""
    t = Table([[""]], colWidths=[width], rowHeights=[1])
    t.setStyle(TableStyle([("LINEBELOW", (0, 0), (-1, -1), 0.5, RULE)]))
    return t


def _esc(value: Any) -> str:
    """Escape one dynamic value for a Paragraph that also carries markup.

    `_para` cannot be used where this file adds its own `<font>` tags, so the
    same guarantee lives here: JSON from the LLM or the digest row routinely
    carries an explicit null, and both `html.escape(None)` and `str.strip` on
    an int raise — which `build_pdf` would turn into a silently missing PDF.
    """
    return html.escape(str(value if value is not None else ""))


def _para(text: Any, style) -> Paragraph:
    """The one door dynamic text takes to ReportLab.

    Paragraph parses a mini-XML dialect, so an LLM-written finding containing
    a stray `</font>` aborts the whole document and a `<nil>` is swallowed as
    a tag. Escaping here rather than at each call site means a new section
    cannot quietly reintroduce the problem. Markup this file adds on purpose
    is composed around `_para`-escaped values, never passed through it.
    """
    return Paragraph(_esc(text), style)


def _finding_flowable(f: Dict[str, Any], width: float, st) -> Table:
    """A finding as a panel with its severity band down the left edge.

    LINEBEFORE draws the band; the table is the only flowable that can put a
    rule beside content rather than above or below it.
    """
    inner = [_para(f["title"], st["cell"]), _para(f["meta"], st["meta"])]
    if f["problem"]:
        inner.append(_para(f["problem"], st["body"]))
    if f["fix"]:
        inner.append(Paragraph(f"<font color='#6B7280'>Fix —</font> {_esc(f['fix'])}", st["body"]))

    t = Table([[inner]], colWidths=[width])
    t.setStyle(
        TableStyle(
            [
                ("LINEBEFORE", (0, 0), (0, -1), 3, f["band"]),
                ("BACKGROUND", (0, 0), (-1, -1), PANEL),
                ("LEFTPADDING", (0, 0), (-1, -1), 7),
                ("RIGHTPADDING", (0, 0), (-1, -1), 7),
                ("TOPPADDING", (0, 0), (-1, -1), 6),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 6),
            ]
        )
    )
    return t


def _page_footer(context: Dict[str, Any]):
    label = f"{context['brand']} weekly digest · {context['period_label']}"

    def draw(canvas, doc):
        canvas.saveState()
        canvas.setFont("Helvetica", 7.5)
        canvas.setFillColor(colors.HexColor("#9AA3AF"))
        canvas.drawString(doc.leftMargin, 12 * mm, label)
        canvas.drawRightString(A4[0] - doc.rightMargin, 12 * mm, f"page {canvas.getPageNumber()}")
        canvas.restoreState()

    return draw


def _section(title: str, width: float, st) -> List[Any]:
    return [_para(title, st["h2"]), _rule(width), Spacer(1, 6)]


def _header_story(context: Dict[str, Any], st) -> List[Any]:
    scope = f"{context['brand']} · {context['period_label']}"
    if context.get("accounts"):
        scope += f" · {context['accounts']} account" + ("" if context["accounts"] == 1 else "s")
    story = [
        _para("Weekly digest", st["title"]),
        _para(scope, st["sub"]),
        _para(context["verdict"], st["verdict"]),
    ]
    if context["lede"]:
        story.append(_para(context["lede"], st["lede"]))
    return story


def _tiles_story(context: Dict[str, Any], width: float, st) -> List[Any]:
    tiles = context["tiles"]
    if not tiles:
        return []
    cells = [
        [_para(v, st["tileValue"]) for _, v in tiles],
        [_para(la, st["tileLabel"]) for la, _ in tiles],
    ]
    col = width / len(tiles)
    table = Table(cells, colWidths=[col] * len(tiles))
    table.setStyle(TableStyle([("LEFTPADDING", (0, 0), (-1, -1), 0), ("BOTTOMPADDING", (0, 0), (-1, -1), 2)]))
    return _section("THE WEEK IN NUMBERS", width, st) + [table]


def _findings_story(context: Dict[str, Any], width: float, st) -> List[Any]:
    findings = context["findings"]
    if not findings:
        return []
    story = _section(f"WHAT BROKE · {len(findings)}", width, st)
    for f in findings:
        story.append(_finding_flowable(f, width, st))
        story.append(Spacer(1, 6))
    return story


def _patterns_story(context: Dict[str, Any], width: float, st) -> List[Any]:
    patterns = context["patterns"]
    if not patterns:
        return []
    story = _section("PATTERNS", width, st)
    for p in patterns:
        head = _esc(p.get("title"))
        if p.get("stance"):
            head += f" <font color='#6B7280'>· {_esc(p['stance'])}</font>"
        story.append(KeepTogether([Paragraph(head, st["cell"]), _para(p.get("body", ""), st["body"]), Spacer(1, 6)]))
    return story


def _plan_story(context: Dict[str, Any], width: float, st) -> List[Any]:
    plan = context["plan"]
    if not plan:
        return []
    rows = [[_para(h, st["th"]) for h in ("PRIORITY", "ACTION", "AREA", "OWNER")]]
    for item in plan:
        rows.append(
            [
                _para(item.get("priority", ""), st["cell"]),
                _para(item.get("action", ""), st["cell"]),
                _para(item.get("area", ""), st["cellMuted"]),
                _para(item.get("owner", ""), st["cellMuted"]),
            ]
        )
    table = Table(rows, colWidths=[width * 0.12, width * 0.56, width * 0.16, width * 0.16], repeatRows=1)
    table.setStyle(
        TableStyle(
            [
                ("LINEBELOW", (0, 0), (-1, 0), 0.5, RULE),
                ("LINEBELOW", (0, 1), (-1, -1), 0.25, colors.HexColor("#EEF1F4")),
                ("VALIGN", (0, 0), (-1, -1), "TOP"),
                ("LEFTPADDING", (0, 0), (-1, -1), 0),
                ("RIGHTPADDING", (0, 0), (-1, -1), 6),
                ("TOPPADDING", (0, 0), (-1, -1), 4),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 4),
            ]
        )
    )
    return _section("PLAN FOR NEXT WEEK", width, st) + [table]


def _carryover_story(context: Dict[str, Any], width: float, st) -> List[Any]:
    carried, resolved = context["carried_over"], context["resolved"]
    if not (carried or resolved):
        return []

    def name(c):
        return f"{c.get('aggregation_key') or ''}" + (f" ({c['account_name']})" if c.get("account_name") else "")

    story = _section("CARRIED OVER & RESOLVED", width, st)
    if carried:
        items = ", ".join(f"{_esc(name(c))} · {c.get('weeks') or 0}w" for c in carried)
        story.append(Paragraph(f"<font color='#6B7280'>Still open:</font> {items}", st["body"]))
    if resolved:
        story.append(
            Paragraph(
                f"<font color='#6B7280'>Stopped recurring:</font> " f"{', '.join(_esc(name(c)) for c in resolved)}",
                st["body"],
            )
        )
    return story


def _hygiene_story(context: Dict[str, Any], width: float, st) -> List[Any]:
    if not context["hygiene"]:
        return []
    story = _section("SIGNAL HYGIENE", width, st)
    for label, value in context["hygiene"]:
        story.append(Paragraph(f"<font color='#6B7280'>{_esc(label)} —</font> {_esc(value)}", st["body"]))
    return story


def build_pdf(context: Dict[str, Any]) -> Optional[bytes]:
    """Draw the review. Returns None on failure rather than raising.

    A PDF is an enhancement to the message, never its point: if generation
    fails, the summary must still go out — the caller falls back to posting the
    review link instead.
    """
    try:
        buffer = BytesIO()
        doc = SimpleDocTemplate(
            buffer,
            pagesize=A4,
            leftMargin=15 * mm,
            rightMargin=15 * mm,
            topMargin=16 * mm,
            bottomMargin=18 * mm,
            title=f"Weekly digest · {context['period_label']}",
        )
        st = _styles()
        width = doc.width
        story: List[Any] = _header_story(context, st)
        for section in (_tiles_story, _findings_story, _patterns_story, _plan_story, _carryover_story, _hygiene_story):
            story.extend(section(context, width, st))

        footer = _page_footer(context)
        doc.build(story, onFirstPage=footer, onLaterPages=footer)
    except Exception:
        LOG.exception("weekly digest: PDF rendering failed")
        return None

    data = buffer.getvalue()
    if not data:
        LOG.error("weekly digest: PDF rendering produced no bytes")
        return None
    if len(data) > MAX_PDF_BYTES:
        LOG.error("weekly digest: PDF is %d bytes, over the %d cap", len(data), MAX_PDF_BYTES)
        return None
    return data


def report_filename(period_start: str) -> str:
    return f"weekly-digest-{period_start}.pdf"
