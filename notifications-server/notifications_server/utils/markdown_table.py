"""Detect GitHub-Flavored-Markdown pipe tables in LLM responses and render
them with Slack's native ``table`` Block Kit block (a bordered grid with an
auto-styled header row), instead of leaving raw ``| a | b |`` text for
Slack's mrkdwn renderer, which has no table syntax and would show
misaligned literal pipe characters.

llm-server has no dedicated table format: the LLM is simply prompted to emit
GFM pipe tables as part of its plain-text response, and llm-server's own
Slack markdown conversion pass explicitly does not touch them - they arrive
here untouched, alongside any ```mermaid fences handled by mermaid_chart.py.
"""

import re
from dataclasses import dataclass
from typing import Any, Dict, List, Optional, Tuple

from notifications_server.message_templates.blocks import BaseBlock, ContextBlock, GridTableBlock, LinkProp, LinksBlock

_SEPARATOR_CELL_RE = re.compile(r"^:?-+:?$")
_LINK_CELL_RE = re.compile(r"^\[([^\]]+)\]\((\S+)\)$")
_NUMBER_RE = re.compile(r"^-?\d+(\.\d+)?$")
# GFM lets a cell escape a literal pipe as \|. Splitting naively on every "|"
# would break that cell in two and corrupt the row's column count, so split
# only on unescaped pipes, then unescape \| back to | in each resulting cell.
_UNESCAPED_PIPE_RE = re.compile(r"(?<!\\)\|")
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


def _build_cell(text: str) -> Dict[str, Any]:
    # Truncate the parsed content (label/plain text), not the raw string -
    # truncating raw text first could cut a long URL mid-pattern and break
    # link detection, turning a clean link into garbled raw text.
    match = _LINK_CELL_RE.match(text.strip())
    if match:
        label, url = match.groups()
        return {
            "type": "rich_text",
            "elements": [
                {
                    "type": "rich_text_section",
                    "elements": [{"type": "link", "url": url, "text": _truncate_cell_text(label)}],
                }
            ],
        }
    return {"type": "raw_text", "text": _truncate_cell_text(text)}


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
