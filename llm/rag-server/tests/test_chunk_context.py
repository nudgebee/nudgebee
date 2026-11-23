"""Chunk context (#42036): each chunk is embedded and reranked with the page
title, its heading path and, inside a table, the table's header row.

A table row such as "| DynamoDB | ✓ | Default |" carried no hint of which
provider or page it belonged to, so "Does NudgeBee support AWS DynamoDB?"
scored 0.000 against it. The stored chunk text must not change:
knowledge_document.join_chunks rebuilds a page from its chunks' exact overlap.
"""

import hashlib

import pytest

from controllers.knowledge_document import _normalise, join_chunks
from rag.core.documents import processing, scraper
from rag.core.llm import local_reranker as lr
from rag.core.types import Document
from utils.config import Config

AWS_ROWS = "\n".join(f"| Service{i:02d} | ✓ | Default | check {i}, another check | — | Dedicated |" for i in range(30))
PAGE = (
    "Title: Supported Cloud Services | NudgeBee\n\n"
    "# Supported Cloud Services\u200b\n\n"
    "This page lists the services NudgeBee collects.\n\n"
    "## Azure\u200b\n\n"
    "Azure services are listed by ARM type.\n\n"
    "## AWS\u200b\n\n"
    "| Service | Inventory | Metrics | Checks | Fixes | Events |\n"
    "|---|---|---|---|---|---|\n"
    f"{AWS_ROWS}\n\n"
    "### Notes\n\n" + ("A long closing paragraph about fixes and access modes. " * 12)
)


def _chunks(page=PAGE, title="Supported Cloud Services | NudgeBee"):
    return processing._chunk_long_documents([Document(page_content=page, metadata={"title": title})])


def test_chunks_still_join_back_to_the_page():
    chunks = _chunks()
    assert len(chunks) > 3
    assert join_chunks([c.page_content for c in chunks]) == _normalise(PAGE)


def test_table_chunk_carries_path_and_table_header():
    row_chunk = next(c for c in _chunks() if "| Service20 |" in c.page_content)
    assert row_chunk.metadata["chunk_context"] == (
        "Supported Cloud Services | NudgeBee › AWS\n| Service | Inventory | Metrics | Checks | Fixes | Events |"
    )


def test_chunk_after_a_subheading_has_the_nested_path_and_no_table_header():
    last = _chunks()[-1]
    assert last.metadata["chunk_context"] == "Supported Cloud Services | NudgeBee › AWS › Notes"


def test_h1_repeating_the_title_and_zero_width_spaces_are_dropped():
    contexts = [c.metadata.get("chunk_context", "") for c in _chunks()]
    assert all("\u200b" not in c for c in contexts)
    assert not any("› Supported Cloud Services" in c for c in contexts)


def test_title_comes_from_the_title_line_when_metadata_has_none():
    chunks = _chunks(title="")
    assert chunks[-1].metadata["chunk_context"].startswith("Supported Cloud Services | NudgeBee ›")


def test_long_table_is_cut_at_row_boundaries():
    # Each chunk's own text (after the carried overlap) starts at a row start,
    # so no service name is split across two chunks.
    chunks = _chunks()
    for i, chunk in enumerate(chunks[1:], start=1):
        body = chunk.page_content[processing._CHUNK_OVERLAP_CHARS :].lstrip("\n")
        if "| Service" in chunk.page_content and body.startswith("|"):
            assert body.startswith("| Service"), f"chunk {i} starts mid-row: {body[:40]!r}"


def test_stored_text_is_identical_with_context_off(monkeypatch):
    with_context = [c.page_content for c in _chunks()]
    monkeypatch.setattr(Config, "chunk_context_enabled", False)
    without = _chunks()
    assert [c.page_content for c in without] == with_context
    assert all("chunk_context" not in c.metadata and c.id is None for c in without)


def test_point_id_hashes_context_and_text():
    chunk = next(c for c in _chunks() if c.metadata.get("chunk_context"))
    expected = hashlib.md5(f"{chunk.metadata['chunk_context']}\n\n{chunk.page_content}".encode()).hexdigest()
    assert chunk.id == expected
    assert processing._content_id(chunk) == expected


def test_retrieval_text_puts_context_first():
    doc = Document(page_content="| DynamoDB | ✓ |", metadata={"chunk_context": "Page › AWS"})
    assert doc.retrieval_text() == "Page › AWS\n\n| DynamoDB | ✓ |"
    assert Document(page_content="plain").retrieval_text() == "plain"
    assert Document(page_content=None, metadata={}).retrieval_text() == ""


def test_line_cut_prefers_a_line_break_in_the_second_half():
    text = "a" * 30 + "\n" + "b" * 30
    assert processing._line_cut(text, limit=40, overlap=10) == 31
    # No line break in the second half of the window: fixed-width cut.
    assert processing._line_cut("c" * 60, limit=40, overlap=10) == 40
    assert processing._line_cut("d\n" + "e" * 60, limit=40, overlap=10) == 40


def test_reranker_scores_context_with_the_chunk(monkeypatch):
    seen = []

    class _Model:
        def predict(self, pairs, **_):
            seen.extend(pairs)
            return [0.9 for _ in pairs]

    monkeypatch.setattr(lr, "get_reranker", lambda: _Model())
    doc = Document(page_content="| DynamoDB | ✓ |", metadata={"chunk_context": "Page › AWS"})
    lr.rerank("dynamodb", [(doc, 0.5)])
    assert seen == [("dynamodb", "Page › AWS\n\n| DynamoDB | ✓ |")]


class _Resp:
    def __init__(self, body: bytes, content_type: str):
        self.headers = {"Content-Type": content_type}
        self.content = body
        self.encoding = "ISO-8859-1"  # what requests picks for text/* without a charset

    @property
    def text(self):
        return self.content.decode(self.encoding)


@pytest.mark.parametrize("content_type", ["text/html", "text/html; charset=utf-8"])
def test_docs_page_without_charset_decodes_as_utf8(content_type):
    resp = _Resp("<p>✓ — ok</p>".encode("utf-8"), content_type)
    if "charset" in content_type:
        resp.encoding = "utf-8"
    assert scraper._response_html(resp) == "<p>✓ — ok</p>"


def test_docs_page_keeps_a_declared_charset():
    resp = _Resp("<p>caf\xe9</p>".encode("latin-1"), "text/html; charset=ISO-8859-1")
    assert scraper._response_html(resp) == "<p>café</p>"


def test_comment_in_a_code_fence_is_not_a_heading():
    page = "## Fix\n\n```bash\n# restart the pods\nkubectl rollout restart\n```\n\n" + "Then wait. " * 40
    assert processing._context_at(page, len(page) - 10, "Runbook") == "Runbook › Fix"


def test_bare_hashes_line_is_not_a_heading():
    page = "## Steps\n\n##\nfoo\n\n" + "body " * 40
    assert processing._context_at(page, len(page) - 5, "T") == "T › Steps"


def test_reranker_window_keeps_the_whole_chunk_and_drops_context_lines_first():
    doc = Document(page_content="x" * 400, metadata={"chunk_context": "P" * 60 + "\n" + "H" * 60})
    # Path fits beside the chunk, the header does not.
    assert doc.retrieval_text(500) == "P" * 60 + "\n\n" + "x" * 400
    # No room at all: the chunk alone, cut to the window.
    assert doc.retrieval_text(400) == "x" * 400
    long_chunk = Document(page_content="y" * 600, metadata={"chunk_context": "P"})
    assert long_chunk.retrieval_text(500) == "y" * 500
    # Everything fits: unchanged.
    assert doc.retrieval_text(10_000) == doc.retrieval_text()


def test_embedding_migration_embeds_the_context_too():
    from rag.core.documents import embedding_migration

    payload = {"page_content": "| DynamoDB | ✓ |", "metadata": {"chunk_context": "Page › AWS"}}
    assert embedding_migration._embedding_text_of(payload) == "Page › AWS\n\n| DynamoDB | ✓ |"
    assert embedding_migration._embedding_text_of({"page_content": "plain"}) == "plain"
    assert embedding_migration._embedding_text_of(None) == ""


def test_labels_stored_under_the_legacy_id_count_as_classified(monkeypatch):
    doc = Document(page_content=PAGE, metadata={"title": "Supported Cloud Services | NudgeBee"})
    pairs = processing._chunk_id_pairs([doc])
    assert any(new != legacy for new, legacy in pairs)
    legacy_labels = {legacy: {"kind": "reference"} for _, legacy in pairs}
    monkeypatch.setattr(processing, "_classified_labels", lambda _collection, ids: dict(legacy_labels))
    classified = []
    monkeypatch.setattr(processing, "_classify_source_documents", lambda groups, *_: classified.extend(groups))

    stored = processing._classify_kb_source_documents([doc], "knowledge_base", "acct", "kb_x", False)

    assert classified == []  # nothing re-sent to the classifier
    assert all(stored[new] == {"kind": "reference"} for new, _ in pairs)


@pytest.mark.parametrize(
    "page, expected",
    [
        ("# A\n````md\n```\n# not\n```\n````\n## B\nXX", "T › A › B"),  # ``` inside ```` stays code
        ("# A\n```\n~~~\n# in\n```\n## B\nXX", "T › A › B"),  # ~~~ inside ``` stays code
        ("# A\n~~~\n```\n~~~\n## B\nXX", "T › A › B"),
        ("# A\n```\n# in\n```js\n# still\n```\n## B\nXX", "T › A › B"),  # a closer has no info string
        ("# A\n    ```\n## B\nXX", "T › A › B"),  # indented 4: not a fence
    ],
)
def test_fences_follow_commonmark(page, expected):
    assert processing._context_at(page, page.index("XX"), "T") == expected
