from notifications_server.message_templates.blocks import ContextBlock, GridTableBlock, LinksBlock
from notifications_server.utils.markdown_table import render_table, split_table_segments


def _text_cell(text):
    return {"type": "raw_text", "text": text}


TABLE_TEXT = """| Resource | Current ($) | Previous ($) | Change (%) |
|---|---|---|---|
| EC2 | 120 | 100 | 20 |
| RDS | 80 | 90 | -11 |
"""


class TestSplitTableSegments:
    def test_no_table_returns_single_text_segment(self):
        segments = split_table_segments("just plain text")
        assert len(segments) == 1
        assert segments[0].is_table is False
        assert segments[0].text == "just plain text"

    def test_extracts_table_between_text(self):
        text = f"before\n\n{TABLE_TEXT}\nafter"
        segments = split_table_segments(text)
        assert [s.is_table for s in segments] == [False, True, False]
        assert "EC2" in segments[1].text
        assert segments[0].text == "before\n\n"
        assert segments[2].text == "\nafter"

    def test_empty_text_returns_single_empty_segment(self):
        segments = split_table_segments("")
        assert len(segments) == 1
        assert segments[0].is_table is False
        assert segments[0].text == ""

    def test_header_and_separator_without_rows_is_not_a_table(self):
        text = "| A | B |\n|---|---|\n"
        segments = split_table_segments(text)
        assert len(segments) == 1
        assert segments[0].is_table is False

    def test_mismatched_column_count_separator_is_not_a_table(self):
        text = "| A | B |\n|---|\n| 1 | 2 |\n"
        segments = split_table_segments(text)
        assert len(segments) == 1
        assert segments[0].is_table is False

    def test_prose_line_with_a_single_pipe_is_not_treated_as_a_header(self):
        text = "Use `a | b` to separate values.\nMore text.\n"
        segments = split_table_segments(text)
        assert len(segments) == 1
        assert segments[0].is_table is False

    def test_prose_line_after_table_with_stray_pipe_is_not_absorbed_as_a_row(self):
        # A line with an unbalanced pipe (no trailing "|") is prose, not a
        # continuation of the table - the old bare "|" in line check would
        # incorrectly swallow it as an extra data row.
        text = "| A | B |\n|---|---|\n| 1 | 2 |\n| note: use a | b syntax\nmore prose\n"
        segments = split_table_segments(text)
        assert [s.is_table for s in segments] == [True, False]
        assert segments[0].text == "| A | B |\n|---|---|\n| 1 | 2 |\n"
        assert segments[1].text == "| note: use a | b syntax\nmore prose\n"

    def test_table_inside_a_fenced_code_example_is_not_detected(self):
        # Real Mermaid/nb-chart fences are already extracted by their own
        # splitters before this module runs, but a generic ``` fence (e.g. a
        # markdown usage example) can still be present. Table-shaped text
        # inside it must stay untouched, not get lifted out and leave the
        # fence's own ``` markers stranded as stray backticks.
        text = "Here:\n```markdown\n| A | B |\n|---|---|\n| 1 | 2 |\n```\ndone\n"
        segments = split_table_segments(text)
        assert len(segments) == 1
        assert segments[0].is_table is False
        assert segments[0].text == text


class TestRenderTable:
    def test_renders_grid_table_block_with_header_row_first(self):
        blocks = render_table(TABLE_TEXT)
        assert len(blocks) == 1
        assert isinstance(blocks[0], GridTableBlock)

        rows = blocks[0].rows
        assert rows[0] == [_text_cell(h) for h in ["Resource", "Current ($)", "Previous ($)", "Change (%)"]]
        assert rows[1] == [_text_cell(c) for c in ["EC2", "120", "100", "20"]]
        assert rows[2] == [_text_cell(c) for c in ["RDS", "80", "90", "-11"]]

    def test_numeric_columns_get_right_aligned(self):
        blocks = render_table(TABLE_TEXT)
        # Resource column (text) stays default-aligned; the three numeric
        # columns (Current/Previous/Change) get right-aligned.
        assert blocks[0].column_settings == [{}, {"align": "right"}, {"align": "right"}, {"align": "right"}]

    def test_currency_and_unit_formatted_columns_get_right_aligned(self):
        # Real FinOps tables use formatted cells like "$1,200.00" and "45%",
        # not bare digits - the numeric check must see through that
        # formatting for right-alignment to actually fire on them.
        text = (
            "| Service | Cost | Change | Multiplier | Storage |\n"
            "|---|---|---|---|---|\n"
            "| EC2 | $1,200.00 | 45% | 1.2k | 120 GiB |\n"
        )
        blocks = render_table(text)
        assert blocks[0].column_settings == [
            {},
            {"align": "right"},
            {"align": "right"},
            {"align": "right"},
            {"align": "right"},
        ]

    def test_pads_short_rows_to_header_width(self):
        text = "| A | B | C |\n|---|---|---|\n| 1 | 2 |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1] == [_text_cell("1"), _text_cell("2"), _text_cell("")]

    def test_truncates_long_rows_to_header_width(self):
        text = "| A | B |\n|---|---|\n| 1 | 2 | 3 |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1] == [_text_cell("1"), _text_cell("2")]

    def test_no_table_returns_empty_list(self):
        assert render_table("just plain text") == []

    def test_escaped_pipe_in_cell_is_not_split_or_dropped(self):
        # GFM lets a cell escape a literal pipe as \|. Splitting naively on
        # every "|" would break "a\|b" into two cells ("a\" and "b"), and the
        # extra cell would then be silently dropped by row-width
        # normalization instead of just corrupting the column count.
        text = "| Name | Formula |\n|---|---|\n| foo | a\\|b |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1] == [_text_cell("foo"), _text_cell("a|b")]

    def test_table_with_link_cell_becomes_rich_text_link(self):
        text = "| Name | Link |\n|---|---|\n| foo | [view](https://example.com) |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1][0] == _text_cell("foo")
        assert blocks[0].rows[1][1] == {
            "type": "rich_text",
            "elements": [
                {
                    "type": "rich_text_section",
                    "elements": [{"type": "link", "url": "https://example.com", "text": "view"}],
                }
            ],
        }

    def test_caps_rows_and_columns_to_slack_limits(self):
        many_cols = " | ".join(f"c{i}" for i in range(25))
        sep = "---|" * 25
        many_rows = "\n".join(f"| {' | '.join(str(i) for _ in range(25))} |" for i in range(150))
        text = f"| {many_cols} |\n|{sep}\n{many_rows}\n"
        blocks = render_table(text)

        assert len(blocks[0].rows) == 100  # 1 header + 99 data rows
        assert all(len(row) == 20 for row in blocks[0].rows)

        note = next(b for b in blocks if isinstance(b, ContextBlock))
        assert "99 of 150 rows" in note.text
        assert "20 of 25 columns" in note.text
        assert not any(isinstance(b, LinksBlock) for b in blocks)

    def test_truncation_note_includes_link_when_view_url_given(self):
        many_rows = "\n".join(f"| {i} |" for i in range(150))
        text = f"| A |\n|---|\n{many_rows}\n"
        blocks = render_table(text, view_url="https://app.example.com/table")

        link_block = next(b for b in blocks if isinstance(b, LinksBlock))
        assert link_block.links[0].text == "View all Data"
        assert link_block.links[0].url == "https://app.example.com/table"

    def test_no_truncation_note_when_within_limits(self):
        blocks = render_table(TABLE_TEXT)
        assert not any(isinstance(b, ContextBlock) for b in blocks)

    def test_truncates_long_cell_text(self):
        long_value = "x" * 250  # exceeds the 200-char per-cell cap
        text = f"| A |\n|---|\n| {long_value} |\n"
        blocks = render_table(text)

        cell_text = blocks[0].rows[1][0]["text"]
        assert len(cell_text) == 200
        assert cell_text.endswith("…")

    def test_link_cell_with_long_url_is_not_corrupted_by_truncation(self):
        # Truncating the raw "[label](url)" string before parsing would cut
        # a long URL mid-pattern and break link detection entirely - this
        # confirms truncation only ever touches the visible label, never the URL.
        long_url = "https://example.com/" + "x" * 250
        text = f"| Name | Link |\n|---|---|\n| foo | [view details]({long_url}) |\n"
        blocks = render_table(text)

        link_cell = blocks[0].rows[1][1]
        assert link_cell["type"] == "rich_text"
        link_element = link_cell["elements"][0]["elements"][0]
        assert link_element["url"] == long_url  # untouched, however long
        assert link_element["text"] == "view details"  # short label, unaffected

    def test_drops_rows_to_stay_under_aggregate_char_limit(self):
        # Each data row is ~190 chars (under the per-cell 200 cap, so it
        # survives per-cell truncation) - 60 rows exceeds the 10,000-char
        # aggregate cap even though row/column counts are both well within limits.
        cell_value = "y" * 190
        rows = "\n".join(f"| {cell_value} |" for _ in range(60))
        text = f"| A |\n|---|\n{rows}\n"
        blocks = render_table(text)

        total_chars = sum(len(row[0]["text"]) for row in blocks[0].rows)
        assert total_chars <= 10000
        assert len(blocks[0].rows) < 61  # header + 60 data rows would exceed budget
        assert blocks[0].rows[0][0]["text"] == "A"  # header always survives

        note = next(b for b in blocks if isinstance(b, ContextBlock))
        assert "of 60 rows" in note.text
