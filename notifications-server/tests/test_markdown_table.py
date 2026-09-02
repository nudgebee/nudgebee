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

    def test_table_with_bold_cell_becomes_rich_text_style(self):
        text = "| Name | Status |\n|---|---|\n| foo | **critical** |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1][1] == {
            "type": "rich_text",
            "elements": [
                {
                    "type": "rich_text_section",
                    "elements": [{"type": "text", "text": "critical", "style": {"bold": True}}],
                }
            ],
        }

    def test_table_cell_with_mixed_inline_formatting(self):
        # Underscore, not asterisk, is italic - a lone "*x*" is Slack-native
        # bold (see _INLINE_TOKEN_RE), since llm-server's own markdown-to-Slack
        # pass already rewrites GFM "**bold**" into that form before this code
        # ever sees it.
        text = "| Name | Note |\n|---|---|\n| foo | see _this_ and `that` |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1][1] == {
            "type": "rich_text",
            "elements": [
                {
                    "type": "rich_text_section",
                    "elements": [
                        {"type": "text", "text": "see "},
                        {"type": "text", "text": "this", "style": {"italic": True}},
                        {"type": "text", "text": " and "},
                        {"type": "text", "text": "that", "style": {"code": True}},
                    ],
                }
            ],
        }

    def test_table_cell_with_strikethrough_and_bold_italic(self):
        text = "| Name | Note |\n|---|---|\n| foo | ~~old~~ ***new*** |\n"
        blocks = render_table(text)
        elements = blocks[0].rows[1][1]["elements"][0]["elements"]
        assert elements[0] == {"type": "text", "text": "old", "style": {"strike": True}}
        assert elements[2] == {"type": "text", "text": "new", "style": {"bold": True, "italic": True}}

    def test_table_cell_with_slack_native_single_marker_styles(self):
        # llm-server's own Slack conversion pass (convertMarkdownToSlackMarkdown)
        # rewrites GFM "**bold**"/"~~strike~~" into single-marker Slack-native
        # "*bold*"/"~strike~" before this text ever reaches notifications-server
        # - verified directly against that Go function on 2026-08-12 - so both
        # single- and double-marker spellings must parse to the same style.
        text = "| Name | Note |\n|---|---|\n| foo | *bold* and ~struck~ |\n"
        blocks = render_table(text)
        elements = blocks[0].rows[1][1]["elements"][0]["elements"]
        assert elements[0] == {"type": "text", "text": "bold", "style": {"bold": True}}
        assert elements[2] == {"type": "text", "text": "struck", "style": {"strike": True}}

    def test_table_cell_with_slack_native_bold_italic_combo(self):
        text = "| Name | Note |\n|---|---|\n| foo | _*urgent*_ |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1][1]["elements"][0]["elements"] == [
            {"type": "text", "text": "urgent", "style": {"bold": True, "italic": True}}
        ]

    def test_table_cell_with_real_llm_server_bold_italic_form(self):
        # Traced directly against llm-server's convertMarkdownToSlackMarkdown
        # (executor_response_formatter.go, 2026-08-20): GFM "**_urgent_**"
        # hits its reBold regex first (2 asterisks beat reItalic), capturing
        # "_urgent_" and re-wrapping in single asterisks - producing
        # "*_urgent_*" (asterisk OUTER), not "_*urgent*_" (underscore outer,
        # the test above). Before bold_italic3 existed, this fell through to
        # bold3 instead: bold-only, with the inner underscores left as
        # literal characters in the text ("_urgent_" shown verbatim, bold).
        text = "| Name | Note |\n|---|---|\n| foo | *_urgent_* |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1][1]["elements"][0]["elements"] == [
            {"type": "text", "text": "urgent", "style": {"bold": True, "italic": True}}
        ]

    def test_table_cell_with_bold_italic_underscore_containing_identifier(self):
        # bold_italic3's content must allow underscores, not just exclude
        # them, or a genuine bold+italic FinOps identifier like a bucket
        # name falls through to bold3 (bold only, underscores left literal).
        text = "| Name | Note |\n|---|---|\n| foo | *_my_bucket_name_* |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1][1]["elements"][0]["elements"] == [
            {"type": "text", "text": "my_bucket_name", "style": {"bold": True, "italic": True}}
        ]

    def test_table_cell_with_slack_native_link(self):
        # Slack's own link syntax <url|label> / bare <url> - what GFM
        # "[label](url)" becomes after llm-server's conversion pass.
        text = "| Name | Link |\n|---|---|\n| foo | <https://example.com\\|view> |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1][1] == {
            "type": "rich_text",
            "elements": [
                {
                    "type": "rich_text_section",
                    "elements": [{"type": "link", "url": "https://example.com", "text": "view"}],
                }
            ],
        }

    def test_table_cell_with_slack_native_link_unescaped_pipe_in_multi_column_row(self):
        # Real-world regression: llm-server's convertMarkdownToSlackMarkdown
        # rewrites GFM "[label](url)" to "<url|label>" WITHOUT escaping the
        # "|" it just inserted. In a multi-column row, a naive pipe-split
        # treats that "|" as a column separator, splitting the link across
        # two "columns" - the label and closing ">" then get silently
        # truncated off when the row is normalized back to the header width.
        url = "http://127.0.0.1:3000/optimise?account=abc123&category=RightSizing&search=loki-chunks-cache"
        text = (
            "| Namespace | Resource | Savings | Action |\n"
            "| :--- | :--- | :--- | :--- |\n"
            f"| loki | loki-chunks-cache | $32.70 | <{url}|Right-size CPU/Mem> |\n"
        )
        blocks = render_table(text)
        row = blocks[0].rows[1]
        assert len(row) == 4
        assert row[3] == {
            "type": "rich_text",
            "elements": [
                {
                    "type": "rich_text_section",
                    "elements": [{"type": "link", "url": url, "text": "Right-size CPU/Mem"}],
                }
            ],
        }

    def test_table_cell_with_slack_native_bare_link(self):
        text = "| Name | Link |\n|---|---|\n| foo | <https://example.com> |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1][1]["elements"][0]["elements"] == [
            {"type": "link", "url": "https://example.com", "text": "https://example.com"}
        ]

    def test_angle_bracket_placeholder_is_not_mistaken_for_a_link(self):
        # kubectl output routinely uses "<none>"/"<pending>" as a placeholder
        # for an unset field - these must never be parsed as a Slack link
        # (they have no URL scheme), or Slack would reject the whole message
        # with invalid_blocks for a non-URL href.
        text = "| Name | External-IP |\n|---|---|\n| foo | <none> |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1][1] == _text_cell("<none>")

    def test_snake_case_cell_value_gets_partially_italicized(self):
        # Known, accepted tradeoff (see _INLINE_TOKEN_RE docstring): once a
        # lone "*x*" means bold (to match real Slack-native input), genuine
        # italic requires underscores, which collides with snake_case
        # identifiers like "my_bucket_name". This locks in that documented
        # behavior rather than silently regressing it.
        text = "| Resource |\n|---|\n| my_bucket_name |\n"
        blocks = render_table(text)
        assert blocks[0].rows[1][0]["elements"][0]["elements"] == [
            {"type": "text", "text": "my"},
            {"type": "text", "text": "bucket", "style": {"italic": True}},
            {"type": "text", "text": "name"},
        ]

    def test_truncates_across_multiple_styled_elements(self):
        # The single-element truncation tests above don't exercise
        # _truncate_elements' cross-run budget math - this cell has a plain
        # run followed by a bold run whose *combined* length exceeds the
        # 200-char cap, even though neither run alone does.
        text = "| A |\n|---|\n| " + "y" * 150 + " **" + "z" * 100 + "**" + " |\n"
        blocks = render_table(text)
        elements = blocks[0].rows[1][0]["elements"][0]["elements"]
        assert elements[0] == {"type": "text", "text": "y" * 150 + " "}
        assert elements[1] == {"type": "text", "text": "z" * 48 + "…", "style": {"bold": True}}
        assert sum(len(e["text"]) for e in elements) == 200

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
