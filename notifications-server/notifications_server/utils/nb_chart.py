"""Detect and render llm-server's ```nb-chart fenced JSON blocks (and bare
``` fences carrying the same JSON shape without the tag - real output isn't
always tagged) using Slack's native ``data_visualization`` Block Kit block,
the same primitive mermaid_chart.py uses for Mermaid xychart/pie diagrams.

Unlike Mermaid (a general-purpose tool any agent can call), nb-chart is a
second, unrelated chart convention emitted only by the FinOps/cost-optimization
agent (llm/llm-server/agents/prompts_repo/agent_finops.txt), self-constrained
there to <=12 labels / <=4 series. Schema:
    {"type": "bar|doughnut|pie|line|area", "title": "...", "labels": [...],
     "series": [{"key": "...", "data": [...]}], "format": "gi|usd|percent|number"}
For pie/doughnut, "values": [...] replaces "series". Real model output doesn't
always follow this exactly: pie/doughnut "values" is also accepted as a list
of {"label": ..., "value": ...} objects in place of the top-level "labels"
array - see _render_nb_pie.
"""

import json
import re
from dataclasses import dataclass
from typing import Any, Dict, List, Optional

from notifications_server.message_templates.blocks import BaseBlock, ChartBlock, ContextBlock, LinkProp, LinksBlock

# Same tag-capturing shape as mermaid_chart.py's MERMAID_FENCE_RE -
# [^\s`]* (not \w*) so a hyphenated tag like "nb-chart" is matched and
# bounded as one fence, instead of corrupting the boundary of whatever
# fence comes next (see mermaid_chart.py's regression test for why).
NB_CHART_FENCE_RE = re.compile(r"```([^\s`]*)[ \t]*\r?\n(.*?)```", re.DOTALL)

# Slack's data_visualization block hard caps (mirrors mermaid_chart.py; see
# https://docs.slack.dev/reference/block-kit/blocks/data-visualization-block/).
_SLACK_CATEGORY_LABEL_MAX_CHARS = 20
_SLACK_MAX_SERIES = 12
_SLACK_MAX_DATA_POINTS = 20
_SLACK_MAX_PIE_SEGMENTS = 12

# Slack has no "doughnut" chart type - render it as a pie, the closest
# native equivalent.
_CHART_TYPE_MAP = {"bar": "bar", "line": "line", "area": "area", "pie": "pie", "doughnut": "pie"}

# nb-chart's "format" has no Slack schema equivalent, so fold it into the
# y-axis label as a unit hint rather than dropping it silently.
_FORMAT_UNIT_LABELS = {"usd": "USD ($)", "percent": "%", "gi": "GiB"}


@dataclass
class NbChartSegment:
    is_chart: bool
    text: str


def _is_nb_chart_fence(lang: str, content: str) -> bool:
    """Whether a ``` fence is an nb-chart payload: either explicitly tagged
    ```nb-chart, or untagged with a body that parses as nb-chart-shaped JSON
    (real llm-server output doesn't always include the tag - unlike a raw
    Mermaid fence, JSON has no first-line keyword to sniff, so this checks
    the actual required fields instead)."""
    if lang.lower() == "nb-chart":
        return True
    if lang:
        return False  # an explicit non-nb-chart tag, e.g. ```json, ```python
    try:
        data = json.loads(content)
    except (json.JSONDecodeError, TypeError):
        return False
    return (
        isinstance(data, dict)
        and str(data.get("type", "")).lower() in _CHART_TYPE_MAP
        and isinstance(data.get("labels"), list)
    )


def split_nb_chart_segments(text: str) -> List[NbChartSegment]:
    """Split ``text`` into ordered plain-markdown / raw-nb-chart-json segments."""
    segments: List[NbChartSegment] = []
    pos = 0
    matches = [m for m in NB_CHART_FENCE_RE.finditer(text) if _is_nb_chart_fence(m.group(1), m.group(2))]
    for match in matches:
        if match.start() > pos:
            segments.append(NbChartSegment(False, text[pos : match.start()]))
        segments.append(NbChartSegment(True, match.group(2).strip()))
        pos = match.end()
    if pos < len(text):
        segments.append(NbChartSegment(False, text[pos:]))
    if not segments:
        segments.append(NbChartSegment(False, text))
    return segments


def _truncate_label(label: str, max_chars: int = _SLACK_CATEGORY_LABEL_MAX_CHARS) -> str:
    return label if len(label) <= max_chars else label[: max_chars - 1] + "…"


def _truncation_note(view_url: Optional[str], *parts: str) -> List[BaseBlock]:
    """A note (and optional link) to append after a chart that dropped data to
    fit Slack's chart limits, so the loss is visible rather than silent -
    mirrors mermaid_chart.py's _truncation_note."""
    if not parts:
        return []
    blocks: List[BaseBlock] = [ContextBlock(text=f"_Showing {'; '.join(parts)} (Slack chart limit)._")]
    if view_url:
        blocks.append(LinksBlock(links=[LinkProp(text="View all Data", url=view_url)]))
    return blocks


def _render_nb_pie(
    title: str, raw_labels: Optional[List[Any]], data: Dict[str, Any], view_url: Optional[str] = None
) -> List[BaseBlock]:
    values = data.get("values")
    if not isinstance(values, list) or not values:
        return []
    try:
        if isinstance(raw_labels, list) and raw_labels:
            labels = [_truncate_label(str(v)) for v in raw_labels]
            all_pairs = sorted(zip(labels, (float(v) for v in values)), key=lambda item: item[1], reverse=True)
        elif all(isinstance(v, dict) for v in values):
            # Some agents nest each label/value pair inside "values" (e.g.
            # [{"label": "EC2", "value": 1200}]) instead of the documented
            # parallel top-level "labels" array - accept that shape too
            # rather than silently falling back to raw JSON.
            all_pairs = sorted(
                ((_truncate_label(str(v["label"])), float(v["value"])) for v in values),
                key=lambda item: item[1],
                reverse=True,
            )
        else:
            return []
    except (TypeError, ValueError, KeyError):
        return []

    segments_truncated = len(all_pairs) > _SLACK_MAX_PIE_SEGMENTS
    if segments_truncated:
        # Sort descending and roll the smallest excess into "Other" instead of
        # just dropping the tail in listed order - otherwise the biggest slice
        # could be silently dropped just because of where it fell in the JSON
        # array, and the shown percentages would no longer add up to the true
        # total. Mirrors mermaid_chart.py's _render_pie.
        kept = all_pairs[: _SLACK_MAX_PIE_SEGMENTS - 1]
        rolled_up = all_pairs[_SLACK_MAX_PIE_SEGMENTS - 1 :]
        pairs = kept + [("Other", sum(value for _label, value in rolled_up))]
    else:
        pairs = all_pairs

    chart = {"type": "pie", "segments": [{"label": label, "value": value} for label, value in pairs]}
    blocks: List[BaseBlock] = [ChartBlock(title=title, chart=chart)]
    if segments_truncated:
        blocks.extend(_truncation_note(view_url, f'{len(rolled_up)} smallest segments combined into "Other"'))
    return blocks


def _render_nb_series_chart(
    title: str, slack_type: str, raw_labels: List[Any], data: Dict[str, Any], view_url: Optional[str] = None
) -> List[BaseBlock]:
    series = data.get("series")
    if not isinstance(series, list) or not series:
        return []

    labels = [_truncate_label(str(v)) for v in raw_labels[:_SLACK_MAX_DATA_POINTS]]

    chart_series = []
    for entry in series[:_SLACK_MAX_SERIES]:
        if not isinstance(entry, dict):
            continue
        values = entry.get("data")
        if not isinstance(values, list):
            continue
        try:
            chart_series.append(
                {
                    "name": str(entry.get("key", "")),
                    "data": [{"label": labels[i], "value": float(v)} for i, v in enumerate(values[: len(labels)])],
                }
            )
        except (TypeError, ValueError):
            continue
    if not chart_series:
        return []

    axis_config: Dict[str, Any] = {"categories": labels}
    fmt = str(data.get("format") or "").lower()
    if fmt and fmt != "number":
        axis_config["y_label"] = _FORMAT_UNIT_LABELS.get(fmt, fmt)

    chart = {"type": slack_type, "series": chart_series, "axis_config": axis_config}
    blocks: List[BaseBlock] = [ChartBlock(title=title, chart=chart)]

    notes = []
    if len(series) > _SLACK_MAX_SERIES:
        notes.append(f"{len(chart_series)} of {len(series)} series")
    if len(labels) < len(raw_labels):
        notes.append(f"{len(labels)} of {len(raw_labels)} data points")
    blocks.extend(_truncation_note(view_url, *notes))
    return blocks


def render_nb_chart(code: str, view_url: Optional[str] = None) -> List[BaseBlock]:
    """Render one nb-chart JSON payload as a native Slack ChartBlock.
    Returns an empty list if it doesn't parse into a usable chart (caller
    falls back to showing the raw text)."""
    try:
        data = json.loads(code)
    except (json.JSONDecodeError, TypeError):
        return []
    if not isinstance(data, dict):
        return []

    slack_type = _CHART_TYPE_MAP.get(str(data.get("type", "")).lower())
    if not slack_type:
        return []

    raw_labels = data.get("labels")
    title = str(data.get("title") or "Chart")

    if slack_type == "pie":
        # Unlike bar/line/area, pie/doughnut can also derive labels from
        # {"label", "value"} objects nested in "values" - see _render_nb_pie.
        return _render_nb_pie(title, raw_labels, data, view_url)

    if not isinstance(raw_labels, list) or not raw_labels:
        return []
    return _render_nb_series_chart(title, slack_type, raw_labels, data, view_url)
