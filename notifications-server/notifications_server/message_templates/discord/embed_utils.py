"""Shared helpers for the Discord embed templates.

Discord hard-rejects messages that exceed its embed limits (10 embeds/message,
256-char titles, 4096-char descriptions, 25 fields, 6000 chars total across all
embeds, ...). clamp_embeds() enforces those limits by truncation so a template
can never produce an undeliverable payload.
"""

import copy
from typing import Any, Dict, List, Optional

MAX_EMBEDS = 10
MAX_TITLE_CHARS = 256
MAX_DESCRIPTION_CHARS = 4096
MAX_FIELDS = 25
MAX_FIELD_NAME_CHARS = 256
MAX_FIELD_VALUE_CHARS = 1024
MAX_FOOTER_CHARS = 2048
# Discord's total-characters cap (title + description + field names/values +
# footer, summed over all embeds) is 6000; stay under with a buffer.
MAX_TOTAL_CHARS = 5900

ELLIPSIS = "…"


def _v(value: Any) -> str:
    """Discord rejects empty field values; render None/empty as an em dash."""
    if value is None or value == "":
        return "—"
    return str(value)


def _truncate(text: str, limit: int) -> str:
    if len(text) <= limit:
        return text
    if limit <= len(ELLIPSIS):
        return ELLIPSIS[:limit]
    return text[: limit - len(ELLIPSIS)] + ELLIPSIS


def _embed_char_count(embed: Dict[str, Any]) -> int:
    total = len(embed.get("title") or "") + len(embed.get("description") or "")
    for field in embed.get("fields") or []:
        total += len(str(field.get("name") or "")) + len(str(field.get("value") or ""))
    footer = embed.get("footer") or {}
    total += len(str(footer.get("text") or ""))
    return total


def _total_chars(embeds: List[Dict[str, Any]]) -> int:
    return sum(_embed_char_count(embed) for embed in embeds)


def _clamp_embed(embed: Dict[str, Any]) -> Dict[str, Any]:
    clamped = copy.deepcopy(embed)
    if clamped.get("title"):
        clamped["title"] = _truncate(str(clamped["title"]), MAX_TITLE_CHARS)
    if clamped.get("description"):
        clamped["description"] = _truncate(str(clamped["description"]), MAX_DESCRIPTION_CHARS)
    if clamped.get("fields"):
        fields = clamped["fields"][:MAX_FIELDS]
        for field in fields:
            field["name"] = _truncate(str(field.get("name") or ""), MAX_FIELD_NAME_CHARS)
            field["value"] = _truncate(str(field.get("value") or ""), MAX_FIELD_VALUE_CHARS)
        clamped["fields"] = fields
    footer = clamped.get("footer")
    if footer and footer.get("text"):
        footer["text"] = _truncate(str(footer["text"]), MAX_FOOTER_CHARS)
    return clamped


def clamp_embeds(embeds: List[Dict[str, Any]], content: Optional[str] = None) -> List[Dict[str, Any]]:
    """Return a copy of ``embeds`` guaranteed to fit Discord's message limits.

    Per-embed limits are enforced by truncation (with an ellipsis). The message
    total is then brought under MAX_TOTAL_CHARS by dropping whole trailing
    embeds first, then truncating the last kept embed's description (and, as a
    last resort, its trailing fields). ``content`` is not counted: Discord caps
    it separately at 2000 chars, outside the embed total.
    """
    clamped = [_clamp_embed(embed) for embed in embeds[:MAX_EMBEDS]]

    while len(clamped) > 1 and _total_chars(clamped) > MAX_TOTAL_CHARS:
        clamped.pop()

    if clamped and _total_chars(clamped) > MAX_TOTAL_CHARS:
        last = clamped[-1]
        overshoot = _total_chars(clamped) - MAX_TOTAL_CHARS
        description = last.get("description") or ""
        allowed = max(len(description) - overshoot, 0)
        if allowed > 0:
            last["description"] = _truncate(description, allowed)
        else:
            last.pop("description", None)
        while last.get("fields") and _total_chars(clamped) > MAX_TOTAL_CHARS:
            last["fields"].pop()

    return clamped
