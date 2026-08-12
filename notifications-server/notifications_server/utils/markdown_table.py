"""Detect GitHub-Flavored-Markdown pipe tables in LLM responses and render
them with Slack's native ``table`` Block Kit block (a bordered grid with an
auto-styled header row), instead of leaving raw ``| a | b |`` text for
Slack's mrkdwn renderer, which has no table syntax and would show
misaligned literal pipe characters.

llm-server has no dedicated table format: the LLM is simply prompted to emit
GFM pipe tables as part of its plain-text response. For the InstantNotification
/ Investigation Slack path, llm-server's own markdown-to-Slack-mrkdwn pass
(``convertMarkdownToSlackMarkdown`` in llm-server's executor_response_formatter.go)
runs over the ENTIRE response text before it ever reaches notifications-server
- table cells are not protected/skipped, so GFM markers inside cells
(``**bold**``, ``[x](y)``, ``~~x~~``) already arrive here rewritten into
Slack-native single-marker form (``*bold*``, ``<y|x>``, ``~x~``). Verified
directly against that Go function (2026-08-12), which is why _INLINE_TOKEN_RE
below matches both dialects rather than assuming raw untouched GFM. That Go
function is now gated on the destination actually being Slack (it used to run
unconditionally, corrupting Teams/Google-Chat messages with Slack-only syntax
they can't render - fixed 2026-08-12) - a distinction that doesn't matter
here specifically, since render_table is only ever invoked for the Slack
destination (GridTableBlock is Slack-Block-Kit-specific) and Slack sessions
always go through the conversion.
"""

import re
from dataclasses import dataclass
from typing import Any, Dict, List, Optional, Tuple

from notifications_server.message_templates.blocks import BaseBlock, ContextBlock, GridTableBlock, LinkProp, LinksBlock

_SEPARATOR_CELL_RE = re.compile(r"^:?-+:?$")
_NUMBER_RE = re.compile(r"^-?\d+(\.\d+)?$")
# Inline markers a Slack table cell's rich_text can actually render: links and
# the bold/italic/strike/code *styles* Slack's rich_text_section text elements
# support (see docs.slack.dev/reference/block-kit/blocks/table-block). Each
# regex group below matches one spelling of one marker - GFM (raw LLM output)
# or Slack-native (what llm-server's convertMarkdownToSlackMarkdown rewrites
# GFM into before this code ever sees it, see module docstring):
#
# | Group          | Pattern       | Style/meaning       | Dialect                |
# |----------------|---------------|---------------------|------------------------|
# | link_text/url  | [label](url)  | link                | GFM                    |
# | slack_link_*   | <url\|label>  | link                | Slack-native           |
# | slack_bare_url | <url>         | link (text = url)   | Slack-native           |
# | bold_italic    | ***text***    | bold, italic        | GFM                    |
# | bold_italic2   | _*text*_      | bold, italic        | Slack-native           |
# | bold           | **text**      | bold                | GFM                    |
# | bold2          | __text__      | bold                | GFM                    |
# | strike         | ~~text~~      | strike              | GFM                    |
# | strike2        | ~text~        | strike              | Slack-native           |
# | code           | `text`        | code                | both (identical)       |
# | italic         | _text_        | italic              | both (identical)       |
# | bold3          | *text*        | bold                | Slack-native (see note)|

#
# Notes:
# - render_table is only ever invoked for the Slack destination (GridTableBlock
#   is Slack-Block-Kit-specific), and Slack sessions always go through
#   convertMarkdownToSlackMarkdown - so on REAL traffic today, only the
#   "Slack-native" rows above are actually expected; the "GFM" rows are
#   defensive coverage (hand-authored content, tests, a future non-Slack
#   caller), not something that reaches here unconverted in production.
# - bold3 ("*text*") is genuinely ambiguous: raw GFM would mean italic, but
#   since llm-server already rewrites "**bold**" -> "*bold*" before this code
#   ever sees it, a lone "*text*" in real traffic is overwhelmingly the
#   converted form of GFM bold - so it's resolved as bold, not italic.
# - Because bold3 claims single-asterisk, genuine italic requires underscores
#   (the "italic" row) - unlike an earlier version of this regex, that is NOT
#   excluded for snake_case identifiers ("my_bucket_name" gets its middle
#   segment italicized). Accepted tradeoff, not an oversight.
# - Slack link URLs are scheme-gated (http(s)://, mailto:) so bracket-free
#   angle-bracket placeholders common in kubectl output - "<none>", "<pending>"
#   - are never mistaken for a link.
_SLACK_URL_SCHEME = r"(?:[a-zA-Z][\w+.-]*://|mailto:)"
_INLINE_TOKEN_RE = re.compile(
    r"\[(?P<link_text>[^\]]+)\]\((?P<link_url>\S+)\)"
    rf"|<(?P<slack_link_url>{_SLACK_URL_SCHEME}[^|<>]+)\|(?P<slack_link_text>[^<>]+)>"
    rf"|<(?P<slack_bare_url>{_SLACK_URL_SCHEME}[^<>]+)>"
    r"|\*\*\*(?P<bold_italic>[^*]+?)\*\*\*"
    r"|_\*(?P<bold_italic2>[^*_]+?)\*_"
    r"|\*\*(?P<bold>[^*]+?)\*\*"
    r"|__(?P<bold2>[^_]+?)__"
    r"|~~(?P<strike>[^~]+?)~~"
    r"|`(?P<code>[^`]+?)`"
    r"|\*(?P<bold3>[^*]+?)\*"
    r"|~(?P<strike2>[^~]+?)~"
    r"|_(?P<italic>[^_]+?)_"
)
# GFM lets a cell escape a literal pipe as \|. Splitting naively on every "|"
# would break that cell in two and corrupt the row's column count, so split
# only on unescaped pipes, then unescape \| back to | in each resulting cell.
_UNESCAPED_PIPE_RE = re.compile(r"(?<!\\)\|")
# A Slack-native <url|label> link (see _INLINE_TOKEN_RE) carries its own "|"
# as real content, not a column separator - but a plain pipe-split can't tell
# the difference from the table's own delimiters. Reuse the \| escape
# convention above: pre-escape a link span's internal "|" so it survives
# _split_row's split untouched, then the same unescape step that undoes user
# escaping restores it inside the (now correctly whole) cell. Without this, a
# link with a label lands split across two "columns" and the label - and the
# closing ">" - gets silently truncated off when the row is normalized back
# to the header's width. The (?<!\\) before the pipe matters: without it, a
# pipe someone already hand-escaped would get a second backslash stacked on
# top here and come out the other end of _split_row's unescape still wearing
# one - this only touches a genuinely unescaped separator.
_SLACK_LINK_SPAN_RE = re.compile(rf"<{_SLACK_URL_SCHEME}[^|<>]+(?<!\\)\|[^<>]+>")
# Real FinOps cell values commonly look like "$1,200.00", "45%", "1.2k", or
# "120 GiB" - strip a leading currency symbol, thousands separators, and a
# trailing unit/percent suffix before testing whether what's left is numeric,
# so right-alignment actually fires on the tables it's meant for.
_CURRENCY_PREFIX_RE = re.compile(r"^[$€£¥]")
_UNIT_SUFFIX_RE = re.compile(r"[a-zA-Z%]+$")
# A generic ``` fence (any language, or none) still present by the time this
# module runs - real Mermaid/nb-chart fences were already extracted by their
# own splitters (see module docstring) - must be treated as opaque. Without
# this, a fenced code example that happens to contain pipe-delimited text
# gets misdetected as a real table, and the fence's own ``` markers get
# lifted out and land as stray backticks in the surrounding text segments.
_FENCE_LINE_RE = re.compile(r"^```")

# Slack's table block hard caps (see the doc link above).
_MAX_TABLE_ROWS = 100
_MAX_TABLE_COLUMNS = 20
# Slack rejects the whole message if a table's cell content exceeds this many
# characters in aggregate - confirmed empirically (error
# table_character_count_must_not_exceed_10000, not documented on the page
# linked above). Also cap per-cell, both as a safety margin against the
# aggregate cap and because a huge single cell isn't readable in a table.
_MAX_TABLE_CHARS = 10000
_MAX_CELL_CHARS = 200


@dataclass
class TableSegment:
    is_table: bool
    text: str


def _split_row(line: str) -> List[str]:
    line = line.strip()
    line = _SLACK_LINK_SPAN_RE.sub(lambda m: m.group(0).replace("|", "\\|"), line)
    if line.startswith("|"):
        line = line[1:]
    if line.endswith("|"):
        line = line[:-1]
    return [cell.replace("\\|", "|").strip() for cell in _UNESCAPED_PIPE_RE.split(line)]


def _separator_column_count(line: str) -> Optional[int]:
    """Returns the column count if ``line`` is a GFM header-separator row (e.g.
    ``|---|:--:|--:|``), else None."""
    if "|" not in line:
        return None
    cells = _split_row(line)
    if cells and all(_SEPARATOR_CELL_RE.match(cell) for cell in cells):
        return len(cells)
    return None


def _normalize_row(row: List[str], width: int) -> List[str]:
    if len(row) < width:
        return row + [""] * (width - len(row))
    return row[:width]


def _looks_like_table_row(line: str) -> bool:
    """A genuine GFM table row is fully pipe-delimited (starts and ends with
    "|"), unlike prose that merely happens to contain a stray "|" character
    (e.g. "note: use a | b syntax"), which a bare "|" in line check would
    incorrectly swallow as an extra data row."""
    stripped = line.strip()
    return stripped.startswith("|") and stripped.endswith("|")


def _fenced_line_mask(lines: List[str]) -> List[bool]:
    """Per-line: True if the line falls inside (or is the delimiter of) a
    generic ``` fence still present in the text - see _FENCE_LINE_RE."""
    mask = []
    in_fence = False
    for line in lines:
        if _FENCE_LINE_RE.match(line.strip()):
            mask.append(True)
            in_fence = not in_fence
            continue
        mask.append(in_fence)
    return mask


def _find_tables(text: str) -> List[Tuple[int, int, List[str], List[List[str]]]]:
    """Returns ``[(start_offset, end_offset, headers, rows)]`` for every GFM
    pipe table found in ``text``, in document order."""
    lines = text.splitlines(keepends=True)
    line_offsets = []
    offset = 0
    for line in lines:
        line_offsets.append(offset)
        offset += len(line)

    fenced = _fenced_line_mask(lines)

    tables = []
    i = 0
    while i < len(lines) - 1:
        if fenced[i]:
            i += 1
            continue
        header_line = lines[i].strip()
        if (
            "|" in header_line
            and not fenced[i + 1]
            and _separator_column_count(lines[i + 1].strip()) == len(_split_row(header_line))
        ):
            headers = _split_row(header_line)
            rows = []
            j = i + 2
            while j < len(lines) and not fenced[j] and _looks_like_table_row(lines[j]):
                rows.append(_normalize_row(_split_row(lines[j]), len(headers)))
                j += 1
            if rows:
                start = line_offsets[i]
                end = line_offsets[j - 1] + len(lines[j - 1])
                tables.append((start, end, headers, rows))
                i = j
                continue
        i += 1

    return tables


def split_table_segments(text: str) -> List[TableSegment]:
    """Split ``text`` into ordered plain-markdown / GFM-table segments."""
    tables = _find_tables(text)
    if not tables:
        return [TableSegment(False, text)]

    segments: List[TableSegment] = []
    pos = 0
    for start, end, _headers, _rows in tables:
        if start > pos:
            segments.append(TableSegment(False, text[pos:start]))
        segments.append(TableSegment(True, text[start:end]))
        pos = end
    if pos < len(text):
        segments.append(TableSegment(False, text[pos:]))
    return segments


def _truncate_cell_text(text: str, max_chars: int = _MAX_CELL_CHARS) -> str:
    return text if len(text) <= max_chars else text[: max_chars - 1] + "…"


_STYLE_GROUPS = {
    "bold_italic": {"bold": True, "italic": True},
    "bold_italic2": {"bold": True, "italic": True},
    "bold": {"bold": True},
    "bold2": {"bold": True},
    "bold3": {"bold": True},
    "strike": {"strike": True},
    "strike2": {"strike": True},
    "code": {"code": True},
    "italic": {"italic": True},
}


def _tokenize_cell(text: str) -> List[Dict[str, Any]]:
    """Split cell text on _INLINE_TOKEN_RE into rich_text_section elements:
    a link keeps its label as plain text (nested styling inside a link label
    isn't parsed - not worth the complexity for table data), a styled run
    becomes a ``text`` element carrying the matching ``style`` flags, and
    everything else is unstyled plain text. Returns a single unstyled
    element when nothing matched, so callers can cheaply detect "plain"."""
    elements: List[Dict[str, Any]] = []
    pos = 0
    for m in _INLINE_TOKEN_RE.finditer(text):
        if m.start() > pos:
            elements.append({"type": "text", "text": text[pos : m.start()]})
        if m.group("link_text") is not None:
            elements.append({"type": "link", "url": m.group("link_url"), "text": m.group("link_text")})
        elif m.group("slack_link_url") is not None:
            elements.append({"type": "link", "url": m.group("slack_link_url"), "text": m.group("slack_link_text")})
        elif m.group("slack_bare_url") is not None:
            url = m.group("slack_bare_url")
            elements.append({"type": "link", "url": url, "text": url})
        else:
            group_name = next(
                (name for name, val in m.groupdict().items() if val is not None and name in _STYLE_GROUPS), None
            )
            if group_name:
                elements.append({"type": "text", "text": m.group(group_name), "style": _STYLE_GROUPS[group_name]})
            else:
                elements.append({"type": "text", "text": m.group(0)})
        pos = m.end()
    if pos < len(text) or not elements:
        elements.append({"type": "text", "text": text[pos:]})
    return elements


def _truncate_elements(elements: List[Dict[str, Any]], max_chars: int = _MAX_CELL_CHARS) -> List[Dict[str, Any]]:
    """Cap a rich_text_section's total visible characters at max_chars,
    trimming from the end and appending "…" to the last surviving element -
    mirrors _truncate_cell_text but across multiple styled/link runs, so a
    long cell can't blow the aggregate table char budget just because its
    formatting happens to be split into several elements. Never touches a
    link's ``url``, only its visible ``text`` label."""
    total = sum(len(el["text"]) for el in elements)
    if total <= max_chars:
        return elements
    truncated: List[Dict[str, Any]] = []
    budget = max_chars - 1
    for el in elements:
        if budget <= 0:
            break
        text = el["text"]
        if len(text) > budget:
            truncated.append({**el, "text": text[:budget]})
            budget = 0
        else:
            truncated.append(el)
            budget -= len(text)
    truncated[-1] = {**truncated[-1], "text": truncated[-1]["text"] + "…"}
    return truncated


def _build_cell(text: str) -> Dict[str, Any]:
    # Tokenize first, truncate the parsed elements after - truncating the
    # raw string first could cut a long URL mid-pattern and break link
    # detection, turning a clean link into garbled raw text.
    elements = _tokenize_cell(text)
    if len(elements) == 1 and "style" not in elements[0] and elements[0]["type"] == "text":
        return {"type": "raw_text", "text": _truncate_cell_text(elements[0]["text"])}
    elements = _truncate_elements(elements)
    return {
        "type": "rich_text",
        "elements": [{"type": "rich_text_section", "elements": elements}],
    }


def _is_number(value: str) -> bool:
    value = _CURRENCY_PREFIX_RE.sub("", value.strip())
    value = _UNIT_SUFFIX_RE.sub("", value).strip().replace(",", "")
    return bool(_NUMBER_RE.match(value))


def _cell_char_count(cell: Dict[str, Any]) -> int:
    if cell.get("type") == "raw_text":
        return len(cell.get("text", ""))
    if cell.get("type") == "rich_text":
        return sum(len(sub.get("text", "")) for el in cell.get("elements", []) for sub in el.get("elements", []))
    return 0


def _row_char_count(row: List[Dict[str, Any]]) -> int:
    return sum(_cell_char_count(cell) for cell in row)


def _table_char_count(rows: List[List[Dict[str, Any]]]) -> int:
    return sum(_row_char_count(row) for row in rows)


def _truncation_note(view_url: Optional[str], *parts: str) -> List[BaseBlock]:
    """A note (and optional link) to append after a table that dropped data to
    fit Slack's table limits, so the loss is visible rather than silent -
    mirrors mermaid_chart.py's _truncation_note."""
    if not parts:
        return []
    blocks: List[BaseBlock] = [ContextBlock(text=f"_Showing {'; '.join(parts)} (Slack table limit)._")]
    if view_url:
        blocks.append(LinksBlock(links=[LinkProp(text="View all Data", url=view_url)]))
    return blocks


def render_table(text: str, view_url: Optional[str] = None) -> List[BaseBlock]:
    """Render one GFM table segment's raw text as a native Slack GridTableBlock
    (a bordered grid with an auto-styled header row). Returns an empty list
    if the segment doesn't parse into a real table."""
    tables = _find_tables(text)
    if not tables:
        return []
    _start, _end, headers, rows = tables[0]
    raw_row_count, raw_col_count = len(rows), len(headers)

    all_rows = ([headers] + rows)[:_MAX_TABLE_ROWS]
    all_rows = [row[:_MAX_TABLE_COLUMNS] for row in all_rows]

    grid_rows = [[_build_cell(cell) for cell in row] for row in all_rows]

    # Per-cell truncation (inside _build_cell) alone might not be enough for
    # a table with many rows. If still over budget, drop data rows from the
    # bottom (keeping the header) rather than cut mid-row and corrupt the
    # table's column shape. Keep all_rows in sync so column_settings below
    # reflects only the rows actually being sent. Track the running total
    # instead of re-summing every cell on every iteration.
    total_chars = _table_char_count(grid_rows)
    while len(grid_rows) > 1 and total_chars > _MAX_TABLE_CHARS:
        total_chars -= _row_char_count(grid_rows[-1])
        grid_rows.pop()
        all_rows.pop()

    data_rows = all_rows[1:]
    column_settings = [
        {"align": "right"} if data_rows and all(_is_number(row[col]) for row in data_rows) else {}
        for col in range(len(all_rows[0]))
    ]

    blocks: List[BaseBlock] = [GridTableBlock(rows=grid_rows, column_settings=column_settings)]

    notes = []
    shown_row_count = len(grid_rows) - 1
    if shown_row_count < raw_row_count:
        notes.append(f"{shown_row_count} of {raw_row_count} rows")
    shown_col_count = len(all_rows[0]) if all_rows else 0
    if shown_col_count < raw_col_count:
        notes.append(f"{shown_col_count} of {raw_col_count} columns")
    blocks.extend(_truncation_note(view_url, *notes))

    return blocks
