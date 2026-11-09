"""The stable per-page handle shared with llm-server: ``<collection>|<source id>``.

Same recipe as llm-server's ``DocumentKey`` (tools/core/knowledgebase_document_category.go),
which user Fact/SOP marks are already stored against, so a label row and a mark about
one page carry the same key. Both sides are pinned to the vectors in
``docs/fixtures/kb_document_keys.json``.

The scraper's id is preferred over the URL because Confluence rebuilds a URL from the
title, so a rename or space move would orphan a URL-keyed row. A manual knowledge
base's collection (``kb_<id>``) holds one note or upload and has no per-page key.
"""

from typing import Any, Optional

# Same order as collection.SOURCE_ID_KEYS and llm-server's documentSourceIDKeys.
SOURCE_ID_KEYS = ("source_id", "page_id", "kb_number", "sys_id")


def _render(value: Any) -> Optional[str]:
    """One id value as llm-server renders it; None when it is blank or not an id.

    Qdrant returns a Confluence ``page_id`` as a JSON number, which llm-server reads as
    a float64 and prints without a fraction, so a whole float prints that way here too.
    """
    if value is None or isinstance(value, bool):
        return None
    if isinstance(value, float) and value.is_integer():
        value = int(value)
    if isinstance(value, (str, int, float)):
        text = str(value).strip()
        return text or None
    return None


def source_id(metadata: Optional[dict]) -> Optional[str]:
    """The scraper's document id from chunk metadata, or None when it stamped none."""
    for key in SOURCE_ID_KEYS:
        rendered = _render((metadata or {}).get(key))
        if rendered:
            return rendered
    return None


def document_key(collection: str, metadata: Optional[dict] = None, url: Optional[str] = None) -> str:
    """``<collection>|<source id>``, or ``<collection>|<url>`` when there is no id; "" when
    the page cannot be keyed (manual knowledge base, no id and no http(s) url)."""
    collection = (collection or "").strip()
    if not collection or collection.startswith("kb_"):
        return ""
    page_id = source_id(metadata)
    if page_id:
        return f"{collection}|{page_id}"
    trimmed = (url or "").strip()
    if trimmed.startswith(("http://", "https://")):
        return f"{collection}|{trimmed}"
    return ""


def label_key(collection: str, metadata: Optional[dict] = None, url: Optional[str] = None) -> str:
    """The key a page's LABEL row is stored under: the page's ``document_key`` for an
    integration collection, and the collection's own name for a manual knowledge base
    (``kb_<id>``), which is one document. A mark's key always contains ``|`` and never
    exists for ``kb_*``, so the two can never be confused. "" when nothing can be keyed."""
    collection = (collection or "").strip()
    if collection.startswith("kb_"):
        return collection
    return document_key(collection, metadata, url)
