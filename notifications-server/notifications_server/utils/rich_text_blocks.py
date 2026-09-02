"""Shared rendering/sending pipeline for turning markdown-ish text into
native Slack blocks and posting them resiliently.

Used by both the LLM investigation-reply pipeline (services/events.py) and
the generic notification template (message_templates/slack/generic.py),
which need the same Mermaid / nb-chart / GFM-table segment dispatch and the
same divide-and-conquer retry on a Slack block-shape rejection, but differ in
how they chunk leftover plain text and how they issue the actual send call.
"""

import json
import logging
from typing import Any, Callable, List, Optional, Sequence

from slack_sdk.errors import SlackApiError

from notifications_server.message_templates.blocks import BaseBlock, MarkdownBlock
from notifications_server.utils.markdown_table import render_table, split_table_segments
from notifications_server.utils.mermaid_chart import render_mermaid_code, split_mermaid_segments
from notifications_server.utils.nb_chart import render_nb_chart, split_nb_chart_segments
from notifications_server.utils.transformer import MAX_BLOCK_CHARS, Transformer

LOG = logging.getLogger(__name__)

SlackBlock = dict
PlainTextRenderer = Callable[[str], List[SlackBlock]]

# Slack error codes that mean "this exact block shape was rejected" rather
# than a transient/systemic failure (rate limit, expired token, network
# blip) - only these are worth bisecting and retrying in halves.
SLACK_BISECTABLE_ERRORS = {"invalid_blocks", "invalid_blocks_format", "msg_too_long"}

# Slack's hard cap on blocks in a single message is 50; batch_slack_groups
# groups atomic content groups (one chart, one table, one text chunk) up to
# this (deliberately lower) limit instead of always sending one Slack message
# per group. Kept a power of two so a rejected message can be evenly bisected
# down to the single offending block (see send_blocks_with_fallback).
DEFAULT_MAX_BLOCKS_PER_MESSAGE = 32


def to_slack_dicts(blocks: Sequence[BaseBlock]) -> List[SlackBlock]:
    result: List[SlackBlock] = []
    for block in blocks:
        result.extend(Transformer.to_slack(block))
    return result


def render_rich_segments(
    text: str,
    render_plain_text: PlainTextRenderer,
    view_url: Optional[str] = None,
) -> List[List[SlackBlock]]:
    """Split `text` into Mermaid / nb-chart / GFM-table / plain-markdown
    segments and render each into one atomic group of Slack block dicts (one
    diagram, one chart, one table, or whatever `render_plain_text` returns
    for a leftover plain chunk - each dict it returns becomes its own group).
    """
    groups: List[List[SlackBlock]] = []
    for mermaid_segment in split_mermaid_segments(text):
        if mermaid_segment.is_mermaid:
            groups.append(to_slack_dicts(render_mermaid_code(mermaid_segment.text, view_url)))
            continue
        groups.extend(_render_non_mermaid_segment(mermaid_segment.text, render_plain_text, view_url))
    return groups


def _render_non_mermaid_segment(
    text: str, render_plain_text: PlainTextRenderer, view_url: Optional[str]
) -> List[List[SlackBlock]]:
    groups: List[List[SlackBlock]] = []
    for nb_segment in split_nb_chart_segments(text):
        if nb_segment.is_chart:
            chart_blocks = render_nb_chart(nb_segment.text, view_url)
            if chart_blocks:
                groups.append(to_slack_dicts(chart_blocks))
            else:
                # Didn't actually parse into a usable chart - show the raw JSON
                # in a code block (matching mermaid_chart.py's own fallback)
                # instead of as plain mrkdwn, where markdown special characters
                # in the JSON values (*, _, `) would get misinterpreted and
                # mangle the output.
                groups.append(to_slack_dicts([MarkdownBlock(text=f"```\n{nb_segment.text}\n```")]))
            continue
        for table_segment in split_table_segments(nb_segment.text):
            if table_segment.is_table:
                table_blocks = render_table(table_segment.text, view_url)
                if table_blocks:
                    groups.append(to_slack_dicts(table_blocks))
                    continue
                # Didn't parse into rows - fall through as plain text.
            groups.extend([block] for block in render_plain_text(table_segment.text))
    return groups


def batch_slack_groups(
    slack_groups: List[List[SlackBlock]],
    max_blocks_per_message: int = DEFAULT_MAX_BLOCKS_PER_MESSAGE,
) -> List[List[SlackBlock]]:
    """Merge adjacent Slack block-dict groups (one chart, one table, one text
    chunk) into as few messages as possible, splitting only when the next
    group would push a message over `max_blocks_per_message` - instead of
    always sending one Slack message per group, which fragments a single
    answer (prose/table/prose/table/...) into many separate messages, loses
    prose's visual grouping with the table/chart it introduces, and risks
    per-channel rate limiting from the burst of chat.postMessage calls."""
    if not slack_groups:
        return []
    messages: List[List[SlackBlock]] = []
    current: List[SlackBlock] = []
    for group in slack_groups:
        if current and len(current) + len(group) > max_blocks_per_message:
            messages.append(current)
            current = []
        current.extend(group)
    messages.append(current)
    return messages


def fallback_slack_block(block: SlackBlock) -> List[SlackBlock]:
    """Best-effort plain-text substitute for one Slack block that Slack
    rejected outright (e.g. a data_visualization/table payload shape it
    doesn't accept even though it cleared our own local caps). Shows the raw
    underlying data in a code block rather than just a "can't be displayed"
    notice, so the numbers are still visible even when the visual can't
    render."""
    block_type = block.get("type")
    if block_type == "data_visualization":
        notice = f"_\U0001f4ca {block.get('title', 'Chart')} — couldn't be displayed here._"
        raw = json.dumps(block.get("chart", {}), indent=2)
    elif block_type == "table":
        notice = "_This table couldn't be displayed here._"
        raw = json.dumps(block.get("rows", []), indent=2)
    else:
        return [
            {
                "type": "section",
                "text": {"type": "mrkdwn", "text": "_Part of this response couldn't be displayed._"},
            }
        ]

    fenced = Transformer.apply_length_limit(f"```\n{raw}\n```", MAX_BLOCK_CHARS)
    return [
        {"type": "section", "text": {"type": "mrkdwn", "text": notice}},
        {"type": "section", "text": {"type": "mrkdwn", "text": fenced}},
    ]


def send_blocks_with_fallback(blocks: List[SlackBlock], send_fn: Callable[[List[SlackBlock]], Any]) -> None:
    """Send `blocks` as one Slack message via `send_fn(blocks)`. If Slack
    rejects it for a reason specific to this exact block shape
    (SLACK_BISECTABLE_ERRORS, raised by `send_fn` as a SlackApiError), bisect
    the block list and retry each half recursively, narrowing down to
    exactly which block caused the rejection instead of assuming every rich
    block in the message is at fault. A single block that still fails on its
    own becomes a plain-text fallback notice, so only the offending piece
    degrades and every other block still renders natively.

    Any other failure (rate limiting, an expired token, a network blip, ...)
    is not block-specific - bisecting would just retry the same doomed
    request up to ~3x the original block count, worsening exactly the outage
    causing it. Those propagate to the caller unchanged.

    `send_fn` must raise SlackApiError on rejection (not swallow it) for
    bisection to trigger - see the module docstring."""
    if not blocks:
        return
    try:
        send_fn(blocks)
        return
    except SlackApiError as e:
        error_code = e.response.data.get("error") if e.response is not None else None
        if error_code not in SLACK_BISECTABLE_ERRORS:
            raise

        if len(blocks) == 1:
            LOG.error("Slack rejected a block (%s), falling back to plain text", error_code)
            try:
                send_fn(fallback_slack_block(blocks[0]))
            except Exception:
                LOG.exception("Fallback send also failed for the offending block")
            return

        LOG.debug("Message of %d blocks rejected (%s), bisecting to isolate the cause", len(blocks), error_code)
        mid = len(blocks) // 2
        send_blocks_with_fallback(blocks[:mid], send_fn)
        send_blocks_with_fallback(blocks[mid:], send_fn)
