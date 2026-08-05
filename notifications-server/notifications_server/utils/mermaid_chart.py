"""Render Mermaid code fences from LLM responses using native Slack Block Kit
primitives, instead of dumping raw Mermaid syntax as text.

Chart-shaped diagrams (``xychart``/``xychart-beta``, ``pie``) map onto Slack's
native ``data_visualization`` block (see
https://docs.slack.dev/reference/block-kit/blocks/data-visualization-block/),
so those render as real bar/line/pie charts. Structural diagrams (flowchart,
sequenceDiagram, classDiagram, stateDiagram, gantt, timeline, ...) have no
such equivalent in Slack, so they fall back to a clearly labeled code block,
optionally with a link back to the full diagram in the web app.
"""

import re
from dataclasses import dataclass
from typing import Dict, List, Optional, Tuple

from notifications_server.message_templates.blocks import (
    BLOCK_SIZE_LIMIT,
    BaseBlock,
    ChartBlock,
    ContextBlock,
    LinkProp,
    LinksBlock,
    MarkdownBlock,
)

MERMAID_FENCE_RE = re.compile(r"```([^\s`]*)[ \t]*\r?\n(.*?)```", re.DOTALL)

# Recognized first-line keywords for a Mermaid diagram body, used to detect
# fences that lack an explicit ```mermaid language tag (some agents emit a
# bare ``` fence around Mermaid syntax instead of following the tagged
# format the VisualizationAgent's own prompt specifies).
_MERMAID_DIAGRAM_KEYWORDS = {
    "pie",
    "graph",
    "flowchart",
    "sequencediagram",
    "classdiagram",
    "statediagram",
    "statediagram-v2",
    "gantt",
    "timeline",
    "journey",
    "erdiagram",
}

# Slack's data_visualization block hard caps (see the doc link above).
_SLACK_CATEGORY_LABEL_MAX_CHARS = 20
_SLACK_AXIS_LABEL_MAX_CHARS = 50
_SLACK_MAX_SERIES = 12
_SLACK_MAX_DATA_POINTS = 20
_SLACK_MAX_PIE_SEGMENTS = 12

_STRUCTURAL_DIAGRAM_LABELS = {
    "graph": "Flowchart",
    "flowchart": "Flowchart",
    "sequencediagram": "Sequence diagram",
    "classdiagram": "Class diagram",
    "statediagram": "State diagram",
    "statediagram-v2": "State diagram",
    "gantt": "Gantt chart",
    "timeline": "Timeline",
    "journey": "User journey",
    "erdiagram": "Entity-relationship diagram",
}


@dataclass
class MermaidSegment:
    is_mermaid: bool
    text: str


def _is_mermaid_fence(lang: str, content: str) -> bool:
    """Whether a ``` fence is a Mermaid diagram: either explicitly tagged
    ```mermaid, or untagged with a body that starts with a recognized
    diagram keyword (some agents omit the language tag)."""
    if lang.lower() == "mermaid":
        return True
    if lang:
        return False  # an explicit non-mermaid tag, e.g. ```python, ```bash
    for line in content.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("%%"):
            continue
        first_word = stripped.split()[0].lower()
        return first_word.startswith("xychart") or first_word in _MERMAID_DIAGRAM_KEYWORDS
    return False


def split_mermaid_segments(text: str) -> List[MermaidSegment]:
    """Split ``text`` into ordered plain-markdown / raw-mermaid-code segments."""
    segments: List[MermaidSegment] = []
    pos = 0
    matches = [m for m in MERMAID_FENCE_RE.finditer(text) if _is_mermaid_fence(m.group(1), m.group(2))]
    for match in matches:
        if match.start() > pos:
            segments.append(MermaidSegment(False, text[pos : match.start()]))
        segments.append(MermaidSegment(True, match.group(2).strip()))
        pos = match.end()
    if pos < len(text):
        segments.append(MermaidSegment(False, text[pos:]))
    if not segments:
        segments.append(MermaidSegment(False, text))
    return segments


def render_mermaid_code(code: str, view_url: Optional[str] = None) -> List[BaseBlock]:
    """Render one Mermaid code fence's body as native Slack blocks.

    Falls back to a labeled raw code block (with an optional link to view the
    rendered diagram in the web app) for anything that isn't a chart, or that
    fails to parse as one.
    """
    diagram_type = _diagram_type(code)

    blocks: List[BaseBlock] = []
    if diagram_type == "xychart":
        blocks = _render_xychart(code, view_url)
    elif diagram_type == "pie":
        blocks = _render_pie(code, view_url)

    if blocks:
        return blocks
    return _render_fallback(code, diagram_type, view_url)


def _diagram_type(code: str) -> str:
    for line in code.splitlines():
        stripped = line.strip()
        if not stripped or stripped.startswith("%%"):
            continue
        first_word = stripped.split()[0].lower()
        if first_word.startswith("xychart"):
            return "xychart"
        return first_word
    return "diagram"


def _truncate_label(label: str, max_chars: int = _SLACK_CATEGORY_LABEL_MAX_CHARS) -> str:
    return label if len(label) <= max_chars else label[: max_chars - 1] + "…"


def _parse_xychart(code: str) -> Tuple[Optional[str], List[str], Optional[str], List[Tuple[str, str, List[float]]]]:
    """Returns (title, x_axis_categories, y_label, [(kind, series_label, values)])."""
    title = None
    y_label = None
    x_axis: List[str] = []
    series: List[Tuple[str, str, List[float]]] = []

    for line in code.splitlines():
        stripped = line.strip()

        match = re.match(r'^title\s+"(.*)"$', stripped)
        if match:
            title = match.group(1)
            continue
        match = re.match(r"^title\s+(.+)$", stripped)
        if match:
            title = match.group(1).strip().strip('"')
            continue

        match = re.match(r'^y-axis\s+"([^"]*)"', stripped)
        if match:
            y_label = match.group(1)
            continue
        match = re.match(r"^y-axis\s+(.+)$", stripped)
        if match:
            y_label = match.group(1).strip().strip('"')
            continue

        match = re.match(r"^x-axis\s+\[(.*)]$", stripped)
        if match:
            x_axis = [v.strip().strip('"') for v in match.group(1).split(",") if v.strip()]
            continue

        # Series label follows the same quoted-or-unquoted convention as
        # title/y-axis above (real agents don't always quote it).
        match = re.match(r'^(bar|line)\s+(?:"([^"]*)"|([^"\[]+?))\s+\[(.*)]$', stripped)
        if match:
            kind, quoted_label, unquoted_label, raw_values = match.groups()
            label = (quoted_label if quoted_label is not None else unquoted_label).strip()
            values = _parse_numeric_array(raw_values)
            if values:
                series.append((kind, label, values))

    return title, x_axis, y_label, series


def _parse_numeric_array(raw_values: str) -> List[float]:
    values: List[float] = []
    for v in raw_values.split(","):
        v = v.strip()
        if not v:
            continue
        try:
            values.append(float(v))
        except ValueError:
            return []
    return values


def _truncation_note(view_url: Optional[str], *parts: str) -> List[BaseBlock]:
    """A note (and optional link) to append after a chart that dropped data to
    fit Slack's chart limits, so the loss is visible rather than silent."""
    if not parts:
        return []
    blocks: List[BaseBlock] = [ContextBlock(text=f"_Showing {'; '.join(parts)} (Slack chart limit)._")]
    if view_url:
        blocks.append(LinksBlock(links=[LinkProp(text="View all Data", url=view_url)]))
    return blocks


def _render_xychart(code: str, view_url: Optional[str] = None) -> List[BaseBlock]:
    title, x_axis, y_label, series = _parse_xychart(code)
    if not series:
        return []

    raw_points = len(x_axis) if x_axis else max(len(values) for _kind, _label, values in series)
    shown_points = min(raw_points, _SLACK_MAX_DATA_POINTS)
    points_truncated = raw_points > shown_points

    categories = (x_axis or [str(i + 1) for i in range(raw_points)])[:shown_points]
    categories = [_truncate_label(c) for c in categories]

    kinds_in_order = list(dict.fromkeys(kind for kind, _label, _values in series))

    blocks: List[BaseBlock] = []
    for kind in kinds_in_order:
        kind_series_all = [(label, values) for k, label, values in series if k == kind]
        kind_series = kind_series_all[:_SLACK_MAX_SERIES]
        series_truncated = len(kind_series_all) > len(kind_series)

        chart: Dict = {
            "type": kind,
            "series": [
                {
                    "name": label,
                    "data": [{"label": categories[i], "value": v} for i, v in enumerate(values[: len(categories)])],
                }
                for label, values in kind_series
            ],
            "axis_config": {"categories": categories},
        }
        if y_label:
            chart["axis_config"]["y_label"] = _truncate_label(y_label, _SLACK_AXIS_LABEL_MAX_CHARS)

        blocks.append(ChartBlock(title=title or f"{kind.capitalize()} chart", chart=chart))

        notes = []
        if series_truncated:
            notes.append(f"{len(kind_series)} of {len(kind_series_all)} series")
        if points_truncated:
            notes.append(f"{shown_points} of {raw_points} data points")
        blocks.extend(_truncation_note(view_url, *notes))

    return blocks


def _parse_pie(code: str) -> List[Tuple[str, float]]:
    slices: List[Tuple[str, float]] = []
    for line in code.splitlines():
        # Same quoted-or-unquoted convention as title/y-axis/series above -
        # Mermaid also allows an unquoted slice label when it has no spaces.
        match = re.match(r'^(?:"([^"]*)"|([^"\s:]+))\s*:\s*([\d.]+)$', line.strip())
        if match:
            quoted_label, unquoted_label, value = match.groups()
            label = quoted_label if quoted_label is not None else unquoted_label
            slices.append((label, float(value)))
    return slices


def _render_pie(code: str, view_url: Optional[str] = None) -> List[BaseBlock]:
    lines = code.splitlines()
    first_line = lines[0].strip() if lines else ""
    # Title is normally quoted (pie title "..."), but not every agent quotes it.
    title_match = re.match(r"^pie\s+title\s+(.+)$", first_line)

    all_slices = sorted(_parse_pie(code), key=lambda item: item[1], reverse=True)
    if not all_slices:
        return []

    segments_truncated = len(all_slices) > _SLACK_MAX_PIE_SEGMENTS
    if segments_truncated:
        # Roll the smallest slices into "Other" instead of just dropping them,
        # so the pie's own percentages still add up to the true total.
        kept = all_slices[: _SLACK_MAX_PIE_SEGMENTS - 1]
        rolled_up = all_slices[_SLACK_MAX_PIE_SEGMENTS - 1 :]
        slices = kept + [("Other", sum(value for _label, value in rolled_up))]
    else:
        slices = all_slices

    chart = {
        "type": "pie",
        "segments": [{"label": _truncate_label(label), "value": value} for label, value in slices],
    }
    title = title_match.group(1).strip().strip('"') if title_match else "Pie chart"

    blocks: List[BaseBlock] = [ChartBlock(title=title, chart=chart)]
    if segments_truncated:
        blocks.extend(_truncation_note(view_url, f'{len(rolled_up)} smallest segments combined into "Other"'))
    return blocks


def _render_fallback(code: str, diagram_type: str, view_url: Optional[str]) -> List[BaseBlock]:
    label = _STRUCTURAL_DIAGRAM_LABELS.get(diagram_type, "Diagram")
    fenced = f"```\n{code}\n```"
    blocks: List[BaseBlock] = [
        ContextBlock(text=f"\U0001f4ca _{label} generated — Slack can't render diagrams inline._"),
        MarkdownBlock(fenced),
    ]
    # MarkdownBlock hard-truncates at BLOCK_SIZE_LIMIT with no indication -
    # for a large diagram that silently drops the closing fence and leaves
    # the code block unterminated, unlike the chart/table paths above, which
    # always call out truncation explicitly. Flag it here too.
    if len(fenced) > BLOCK_SIZE_LIMIT:
        blocks.append(ContextBlock(text="_Diagram code was too long and got truncated to fit Slack's message limit._"))
    if view_url:
        blocks.append(LinksBlock(links=[LinkProp(text="View all Data", url=view_url)]))
    return blocks
