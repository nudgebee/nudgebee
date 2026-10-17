"""Chunk assembly for PDF documents.

Counterparts to the Go tests these were ported from
(llm-server ``knowledge/knowledgebase_pdf_test.go``), so a behaviour change on
either side shows up as a failing test rather than as two indexes that disagree.
"""

from rag.core.documents.pdf_chunks import (
    build_chunk_texts,
    first_non_empty_line,
    split_text_chunks,
)


def extraction_fixture():
    return [
        {"page": 1, "text": "Rollback procedure\nDrain the node first."},
        {"page": 2, "text": "Topology\nSee figure below."},
    ]


def test_short_text_stays_whole():
    assert split_text_chunks("one\ntwo", 4000) == ["one\ntwo"]


def test_splits_on_line_boundaries():
    line = "a" * 30
    chunks = split_text_chunks("\n".join([line] * 4), 70)

    assert len(chunks) >= 2
    for chunk in chunks:
        assert len(chunk) <= 70
        # A split inside a line would destroy the identifiers that make a chunk
        # findable, so every chunk must be whole lines.
        assert all(part == line for part in chunk.split("\n"))


def test_overlong_line_is_emitted_intact():
    long_line = "b" * 200
    assert split_text_chunks(long_line, 50) == [long_line]


def test_first_non_empty_line():
    assert first_non_empty_line("\n\n  Rollback procedure  \nbody", 120) == "Rollback procedure"
    assert first_non_empty_line("   \n\n", 120) == ""


def test_emits_page_and_figure_chunks():
    figures = [
        {
            "page": 2,
            "index": 1,
            "summary": "Architecture diagram of the checkout path.",
            "transcription": "checkout-svc -> payments-api",
        }
    ]

    chunks = build_chunk_texts("runbook.pdf", extraction_fixture(), 2, figures)

    assert len(chunks) == 3
    figure_chunk = chunks[2]
    for want in (
        "runbook.pdf — page 2, figure 1",
        "Section: Topology",  # anchored to its own page's heading
        "Architecture diagram of the checkout path",  # what the model saw
        "checkout-svc -> payments-api",  # the literal identifiers a query matches
    ):
        assert want in figure_chunk
    assert chunks[0].startswith("runbook.pdf — page 1 of 2")


def test_skips_undescribed_figure():
    # A chunk that only says "there is a figure here" retrieves noise and
    # answers nothing; the shortfall is reported on the KB instead.
    figures = [{"page": 1, "index": 1}]

    chunks = build_chunk_texts("runbook.pdf", extraction_fixture(), 2, figures)

    assert len(chunks) == 2


def test_falls_back_when_filename_missing():
    chunks = build_chunk_texts("  ", extraction_fixture(), 2, None)

    assert chunks and chunks[0].startswith("document — page 1 of 2")


def test_transcription_only_figure_is_still_indexed():
    # The transcription is the half a query actually matches, so a figure the
    # model transcribed but did not summarise must still reach the index.
    figures = [{"page": 1, "index": 1, "transcription": "RB-K8S-MEM-007"}]

    chunks = build_chunk_texts("runbook.pdf", extraction_fixture(), 2, figures)

    assert len(chunks) == 3
    assert "RB-K8S-MEM-007" in chunks[1]
    assert "Figure:" not in chunks[1]


def test_anchor_drops_a_repeated_banner():
    # A running header wraps into one line of the same phrase repeated; anchoring a figure
    # to all twelve copies buries its subject.
    assert first_non_empty_line("Latency dashboard. " * 12, 240) == "Latency dashboard."


def test_anchor_leaves_a_real_heading_alone():
    assert first_non_empty_line("Rollback procedure\nbody", 240) == "Rollback procedure"
    assert first_non_empty_line("Drain, drain again\nbody", 240) == "Drain, drain again"
