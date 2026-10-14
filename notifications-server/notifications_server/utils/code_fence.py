"""Extract generic fenced code blocks from text as atomic segments, so
downstream plain-text chunkers never slice through the middle of a ``` fence.

Mermaid and nb-chart fences are already pulled out earlier in the pipeline
(see rich_text_blocks.py) - what's left here is every other fence, tagged or
bare.
"""

import re
from dataclasses import dataclass
from typing import List

from notifications_server.message_templates.blocks import BLOCK_SIZE_LIMIT, BaseBlock, ContextBlock, MarkdownBlock

# Only matches a complete fence, so one the LLM itself never closed is left
# as plain text instead of swallowing the rest of the message.
_CODE_FENCE_RE = re.compile(r"```([^\s`]*)[ \t]*\r?\n(.*?)```", re.DOTALL)

_FENCE_OVERHEAD = len("```\n\n```")
_TRUNCATION_MARKER = "..."


@dataclass
class CodeFenceSegment:
    is_code_fence: bool
    text: str


def split_code_fence_segments(text: str) -> List[CodeFenceSegment]:
    """Split `text` into ordered plain-text / fenced-code segments."""
    segments: List[CodeFenceSegment] = []
    pos = 0
    for match in _CODE_FENCE_RE.finditer(text):
        if match.start() > pos:
            segments.append(CodeFenceSegment(False, text[pos : match.start()]))
        segments.append(CodeFenceSegment(True, match.group(2).strip()))
        pos = match.end()
    if pos < len(text):
        segments.append(CodeFenceSegment(False, text[pos:]))
    if not segments:
        segments.append(CodeFenceSegment(False, text))
    return segments


def render_code_fence(code: str) -> List[BaseBlock]:
    """Render one fenced code block as a single atomic Slack block.

    Always bare ``` - Slack's mrkdwn doesn't recognize a language tag as a
    fence opener. Truncates the code itself, not the whole fenced string,
    when oversized, so the closing fence always still fits."""
    fenced = f"```\n{code}\n```"
    if len(fenced) < BLOCK_SIZE_LIMIT:
        return [MarkdownBlock(fenced)]

    budget = BLOCK_SIZE_LIMIT - _FENCE_OVERHEAD - len(_TRUNCATION_MARKER) - 1
    fenced = f"```\n{code[:budget]}{_TRUNCATION_MARKER}\n```"
    return [
        MarkdownBlock(fenced),
        ContextBlock(text="_Code block was too long and got truncated to fit Slack's message limit._"),
    ]
