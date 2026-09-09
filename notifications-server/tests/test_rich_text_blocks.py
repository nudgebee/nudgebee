"""Tests for render_rich_segments' upload_image opt-in for flowchart/graph
images (mermaid_chart.py's Image tier). Passing an upload_image callback
gets a diagram uploaded and embedded inline as a native Slack `image` block
right in these same returned groups - see rich_text_blocks.py's module
docstring for why that needs a callback rather than just a bool flag.
"""

from notifications_server.utils import mermaid_chart
from notifications_server.utils.rich_text_blocks import render_rich_segments

FLOWCHART_TEXT = 'before\n```mermaid\ngraph TD\n    A["Start"] --> B["End"]\n```\nafter'


def _plain_text_leaf(text):
    return [{"type": "section", "text": {"type": "mrkdwn", "text": text}}] if text.strip() else []


def _all_texts(groups):
    """Every mrkdwn string across a list of Slack block-dict groups,
    regardless of whether it came from a `section` block (text.text) or a
    `context` block (elements[].text)."""
    texts = []
    for group in groups:
        for block in group:
            if "text" in block:
                texts.append(block["text"]["text"])
            for element in block.get("elements", []):
                texts.append(element.get("text", ""))
    return texts


def _all_image_blocks(groups):
    return [b for group in groups for b in group if b.get("type") == "image"]


class _FakeUploader:
    """Tracks every (filename, contents) it's asked to upload and hands back
    a deterministic fake file id, in call order."""

    def __init__(self):
        self.calls = []

    def __call__(self, filename, contents):
        self.calls.append((filename, contents))
        return f"F{len(self.calls)}"


class TestImageEmbedding:
    def test_no_upload_image_keeps_code_block_fallback(self):
        # upload_image defaults to None - a flowchart must still come back
        # as the ordinary code-block fallback, never attempt an upload,
        # unless the caller explicitly supplies a callback.
        groups = render_rich_segments(FLOWCHART_TEXT, _plain_text_leaf)
        assert _all_image_blocks(groups) == []
        assert any("Flowchart" in t for t in _all_texts(groups))

    def test_successful_render_is_embedded_as_an_inline_image_block(self, monkeypatch):
        monkeypatch.setattr(mermaid_chart, "render_flowchart_image", lambda code: b"fake-png-bytes")
        uploader = _FakeUploader()

        groups = render_rich_segments(FLOWCHART_TEXT, _plain_text_leaf, upload_image=uploader)

        image_blocks = _all_image_blocks(groups)
        assert len(image_blocks) == 1
        assert image_blocks[0]["slack_file"]["id"] == "F1"
        assert uploader.calls == [("diagram.png", b"fake-png-bytes")]
        # No group should carry a data_visualization/chart-shaped block for
        # the diagram - it's a native `image` block, nothing else.
        assert not any(b.get("type") == "data_visualization" for group in groups for b in group)

    def test_failed_render_falls_back_with_no_upload_attempted(self, monkeypatch):
        monkeypatch.setattr(mermaid_chart, "render_flowchart_image", lambda code: None)
        uploader = _FakeUploader()

        groups = render_rich_segments(FLOWCHART_TEXT, _plain_text_leaf, upload_image=uploader)

        assert uploader.calls == []
        assert _all_image_blocks(groups) == []
        assert any("Flowchart" in t for t in _all_texts(groups))

    def test_plain_text_segments_are_preserved_around_the_image(self, monkeypatch):
        monkeypatch.setattr(mermaid_chart, "render_flowchart_image", lambda code: b"fake-png-bytes")

        groups = render_rich_segments(FLOWCHART_TEXT, _plain_text_leaf, upload_image=_FakeUploader())

        texts = _all_texts(groups)
        assert any("before" in t for t in texts)
        assert any("after" in t for t in texts)

    def test_single_diagram_uses_unnumbered_filename(self, monkeypatch):
        # Only one flowchart candidate in the message - keep the plain
        # "diagram.png" name rather than "diagram-1.png", since there's
        # nothing else to disambiguate it from.
        monkeypatch.setattr(mermaid_chart, "render_flowchart_image", lambda code: b"fake-png-bytes")
        uploader = _FakeUploader()

        render_rich_segments(FLOWCHART_TEXT, _plain_text_leaf, upload_image=uploader)

        assert uploader.calls[0][0] == "diagram.png"


TWO_FLOWCHART_TEXT = (
    "one\n"
    '```mermaid\ngraph TD\n    A["A"] --> B["B"]\n```\n'
    "two\n"
    '```mermaid\ngraph TD\n    C["C"] --> D["D"]\n```\n'
    "three"
)

THREE_FLOWCHART_TEXT = (
    "one\n"
    '```mermaid\ngraph TD\n    A["A"] --> B["B"]\n```\n'
    "two\n"
    '```mermaid\ngraph TD\n    C["C"] --> D["D"]\n```\n'
    "three\n"
    '```mermaid\ngraph TD\n    E["E"] --> F["F"]\n```\n'
    "four"
)


class TestMultipleDiagramNumbering:
    def test_two_successful_diagrams_are_numbered_1_and_2(self, monkeypatch):
        monkeypatch.setattr(mermaid_chart, "render_flowchart_image", lambda code: b"fake-png-bytes")
        uploader = _FakeUploader()

        render_rich_segments(TWO_FLOWCHART_TEXT, _plain_text_leaf, upload_image=uploader)

        assert [filename for filename, _contents in uploader.calls] == ["diagram-1.png", "diagram-2.png"]

    def test_middle_failure_does_not_create_a_gap_in_numbering(self, monkeypatch):
        # 3 flowchart-shaped segments, but the middle one (code containing
        # "C") fails to render - the two that DO succeed must still be
        # numbered 1 and 2 (matching the order they're actually embedded),
        # not 1 and 3.
        def fake_render(code):
            return None if '"C"' in code else b"fake-png-bytes"

        monkeypatch.setattr(mermaid_chart, "render_flowchart_image", fake_render)
        uploader = _FakeUploader()

        groups = render_rich_segments(THREE_FLOWCHART_TEXT, _plain_text_leaf, upload_image=uploader)

        assert [filename for filename, _contents in uploader.calls] == ["diagram-1.png", "diagram-2.png"]
        # The middle one fell back to the code block - it never got an
        # image, so there's nothing for a number to disambiguate.
        assert any("Flowchart" in t for t in _all_texts(groups))
