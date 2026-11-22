"""Assemble the documents a PDF is indexed as: one per page of text, one per figure.

Ported from llm-server's ``knowledge/knowledgebase_pdf.go``
(``buildKBPDFChunkTexts``) as part of moving ingestion into this service, and
kept behaviourally identical to it — the Go tests in
``knowledgebase_pdf_test.go`` have counterparts in ``tests/test_pdf_chunks.py``.

A figure gets its own chunk rather than being appended to its page so that a
question answered by the diagram retrieves the diagram, not the page of prose
that happens to surround it. Each chunk carries its own header, because a chunk
is read on its own once retrieval has picked it.

One deliberate difference from the Go: budgets here are counted in characters
rather than bytes. Go's ``len`` is bytes, which would chunk a Japanese or
Cyrillic page at roughly a third of the intended length.
"""

from typing import Any, Dict, List, Optional

# Page text beyond this is split. Comfortably inside every embedding model's
# window while still keeping a page's argument in one vector.
MAX_CHUNK_CHARS = 4000

# How much of a page's own text rides along on a figure chunk, so the figure
# lands near its subject in embedding space instead of floating free.
ANCHOR_CHARS = 240


# Longest phrase treated as a repeatable unit. Banner text is far shorter; beyond this
# the scan costs more than the noise it removes.
_MAX_REPEAT_UNIT = 200


def _collapse_repeats(line: str) -> str:
    """Drop an immediately repeated phrase, keeping one copy.

    A page's first line is not always a heading: running headers and banner text wrap into
    one long line of the same phrase over and over, and anchoring a figure to
    "Latency dashboard. Latency dashboard. Latency dashboard." buries its real subject
    under noise in embedding space. Observed on a real extraction, not hypothetical.
    """
    # A running header is short. Scanning every candidate length on a 10,000-character
    # line is quadratic, and this runs per page inside a single-worker server.
    for size in range(1, min(len(line) // 2, _MAX_REPEAT_UNIT) + 1):
        head = line[:size]
        if not head.strip():
            continue
        repeats = len(line) // size
        # Compared block by block rather than against ``head * repeats``: that builds two
        # whole-line strings on every one of the 200 candidate sizes, where this bails at
        # the first block that differs, which for a line that does not repeat is the second.
        if repeats < 2 or any(line[i : i + size] != head for i in range(size, size * repeats, size)):
            continue
        # The last repetition is usually clipped — a trailing space trimmed, or the line
        # cut at the page width — so a remainder that is the start of another copy still
        # counts as repetition.
        remainder = line[size * repeats :]
        if not remainder or head.startswith(remainder):
            return head.strip()
    return line


def first_non_empty_line(text: str, limit: int = ANCHOR_CHARS) -> str:
    """A page's opening line, which in a technical document is almost always its
    heading — the anchor a figure needs to land near its subject."""
    for line in (text or "").split("\n"):
        line = line.strip()
        if line:
            return _collapse_repeats(line)[:limit]
    return ""


def split_text_chunks(text: str, max_chars: int = MAX_CHUNK_CHARS) -> List[str]:
    """Break page text on line boundaries so no chunk exceeds max_chars."""
    text = (text or "").strip()
    if not text:
        return []
    if len(text) <= max_chars:
        return [text]

    chunks: List[str] = []
    current: List[str] = []
    current_len = 0
    for line in text.split("\n"):
        # A single line longer than the budget is emitted on its own rather than
        # being cut: splitting inside a line breaks the identifiers that make a
        # chunk findable.
        if current_len > 0 and current_len + len(line) + 1 > max_chars:
            joined = "\n".join(current).strip()
            if joined:
                chunks.append(joined)
            current = []
            current_len = 0
        if current_len > 0:
            current_len += 1  # the newline
        current.append(line)
        current_len += len(line)

    joined = "\n".join(current).strip()
    if joined:
        chunks.append(joined)
    return chunks


def _figure_chunk(title: str, page_number: int, figure: Dict[str, Any], anchor: str) -> Optional[str]:
    summary = (figure.get("summary") or "").strip()
    transcription = (figure.get("transcription") or "").strip()
    if not summary and not transcription:
        # Never indexed: a chunk that only says "there is a figure here"
        # retrieves noise and answers nothing. The shortfall is reported on the
        # knowledge base instead.
        return None

    parts = [f"{title} — page {page_number}, figure {figure.get('index', 1)}\n"]
    if anchor:
        parts.append(f"Section: {anchor}\n")
    if summary:
        parts.append(f"\nFigure: {summary}\n")
    if transcription:
        parts.append(f"\nText in figure:\n{transcription}\n")
    return "".join(parts).strip()


def build_chunk_texts(
    filename: str,
    pages: List[Dict[str, Any]],
    page_count: int,
    figures: Optional[List[Dict[str, Any]]] = None,
    max_chunk_chars: int = MAX_CHUNK_CHARS,
) -> List[str]:
    """The texts this document is indexed as.

    ``pages`` is ``[{"page": int, "text": str}, ...]`` and ``figures`` is
    ``[{"page": int, "index": int, "summary": str, "transcription": str}, ...]``
    — the extractor's shape, plus whatever the describer filled in.
    """
    title = (filename or "").strip() or "document"

    by_page: Dict[int, List[Dict[str, Any]]] = {}
    for figure in figures or []:
        by_page.setdefault(int(figure.get("page") or 0), []).append(figure)

    chunks: List[str] = []
    for page in pages:
        number = int(page.get("page") or 0)
        text = page.get("text") or ""
        header = f"{title} — page {number} of {page_count}"

        for part in split_text_chunks(text, max_chunk_chars):
            chunks.append(f"{header}\n\n{part}")

        anchor = first_non_empty_line(text)
        for figure in by_page.get(number, []):
            chunk = _figure_chunk(title, number, figure, anchor)
            if chunk:
                chunks.append(chunk)

    return chunks
