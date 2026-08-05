from notifications_server.message_templates.blocks import ChartBlock, ContextBlock, LinksBlock, MarkdownBlock
from notifications_server.utils.mermaid_chart import render_mermaid_code, split_mermaid_segments

XYCHART = """xychart
    title "Server Performance"
    x-axis ["00:00", "01:00", "02:00"]
    y-axis "Value"
    bar "Requests/sec" [50, 60, 85]
    line "Latency (ms)" [40, 50, 60]
"""

PIE = """pie title "Resource Usage"
    "CPU" : 45
    "Memory" : 30
    "Disk" : 25
"""

FLOWCHART = """graph TD
    S1["API Gateway"] --> S2["Auth Service"]
"""


class TestChartBlockTitleTruncation:
    def test_title_within_limit_is_unchanged(self):
        assert ChartBlock(title="Server Performance", chart={}).title == "Server Performance"

    def test_over_limit_title_is_truncated_with_ellipsis(self):
        title = "Average CPU Utilization Across All Worker Nodes In The Production Cluster"
        assert len(title) > 50

        rendered = ChartBlock(title=title, chart={}).title

        assert len(rendered) == 50
        assert rendered.endswith("…")
        assert rendered == title[:49] + "…"

    def test_mermaid_title_over_limit_is_truncated_end_to_end(self):
        title = "Average CPU Utilization Across All Worker Nodes In The Production Cluster"
        code = f'xychart\n    title "{title}"\n    bar "cores" [2, 4, 6]\n'
        blocks = render_mermaid_code(code)

        assert len(blocks[0].title) == 50
        assert blocks[0].title.endswith("…")


class TestSplitMermaidSegments:
    def test_no_mermaid_returns_single_text_segment(self):
        segments = split_mermaid_segments("just plain text")
        assert len(segments) == 1
        assert segments[0].is_mermaid is False
        assert segments[0].text == "just plain text"

    def test_extracts_mermaid_between_text(self):
        text = f"before\n```mermaid\n{FLOWCHART}```\nafter"
        segments = split_mermaid_segments(text)
        assert [s.is_mermaid for s in segments] == [False, True, False]
        assert segments[1].text == FLOWCHART.strip()
        assert segments[0].text == "before\n"
        assert segments[2].text == "\nafter"

    def test_empty_text_returns_single_empty_segment(self):
        segments = split_mermaid_segments("")
        assert len(segments) == 1
        assert segments[0].is_mermaid is False
        assert segments[0].text == ""

    def test_untagged_fence_with_diagram_keyword_is_detected(self):
        # Some agents emit a bare ``` fence around Mermaid syntax instead of
        # the ```mermaid tagged form the VisualizationAgent's prompt specifies.
        text = f"before\n```\n{FLOWCHART}```\nafter"
        segments = split_mermaid_segments(text)
        assert [s.is_mermaid for s in segments] == [False, True, False]
        assert segments[1].text == FLOWCHART.strip()

    def test_untagged_fence_with_unrelated_content_is_not_mermaid(self):
        text = 'before\n```\nprint("hello")\n```\nafter'
        segments = split_mermaid_segments(text)
        assert len(segments) == 1
        assert segments[0].is_mermaid is False

    def test_fence_with_other_language_tag_is_not_mermaid(self):
        text = f"before\n```python\n{FLOWCHART}```\nafter"
        segments = split_mermaid_segments(text)
        assert len(segments) == 1
        assert segments[0].is_mermaid is False

    def test_hyphenated_language_tag_does_not_corrupt_a_later_fence(self):
        # Regression: \w* can't match a hyphen, so a ```nb-chart fence used to
        # fail to match at its own opening, then spuriously "match" starting
        # at its closing ``` through to the NEXT fence's opening backticks -
        # swallowing that fence's own boundary and hiding it from detection
        # entirely. [^\s`]* fixes this by correctly bounding any tag.
        text = f'before\n```nb-chart\n{{"a": 1}}\n```\nmiddle\n```\n{FLOWCHART}```\nafter'
        segments = split_mermaid_segments(text)
        assert [s.is_mermaid for s in segments] == [False, True, False]
        assert "nb-chart" not in segments[1].text
        assert segments[1].text == FLOWCHART.strip()


class TestRenderXyChart:
    def test_renders_one_chart_block_per_series_kind(self):
        blocks = render_mermaid_code(XYCHART)

        assert len(blocks) == 2
        assert all(isinstance(b, ChartBlock) for b in blocks)

        bar_block = next(b for b in blocks if b.chart["type"] == "bar")
        assert bar_block.title == "Server Performance"
        assert bar_block.chart["series"] == [
            {
                "name": "Requests/sec",
                "data": [
                    {"label": "00:00", "value": 50.0},
                    {"label": "01:00", "value": 60.0},
                    {"label": "02:00", "value": 85.0},
                ],
            }
        ]
        assert bar_block.chart["axis_config"]["categories"] == ["00:00", "01:00", "02:00"]
        assert bar_block.chart["axis_config"]["y_label"] == "Value"

        line_block = next(b for b in blocks if b.chart["type"] == "line")
        assert line_block.chart["series"][0]["name"] == "Latency (ms)"

    def test_truncates_y_label_to_slack_limit(self):
        code = f'xychart\n    y-axis "{"x" * 60}"\n    bar "series" [1, 2, 3]\n'
        blocks = render_mermaid_code(code)
        assert len(blocks[0].chart["axis_config"]["y_label"]) == 50

    def test_synthesizes_categories_when_no_x_axis(self):
        code = 'xychart\n    bar "series" [1, 2, 3]\n'
        blocks = render_mermaid_code(code)
        assert blocks[0].chart["axis_config"]["categories"] == ["1", "2", "3"]

    def test_unquoted_series_label_is_parsed(self):
        # Mermaid also allows an unquoted series label (bar Requests [...]),
        # same convention title/y-axis already support - without this, an
        # unquoted label meant no series matched, and the whole chart fell
        # back to a raw code block instead of rendering.
        code = 'xychart\n    x-axis ["00:00", "01:00"]\n    bar Requests [50, 60]\n'
        blocks = render_mermaid_code(code)
        assert isinstance(blocks[0], ChartBlock)
        assert blocks[0].chart["series"][0]["name"] == "Requests"

    def test_mixed_quoted_and_unquoted_series_labels_both_parsed(self):
        code = 'xychart\n    x-axis ["00:00", "01:00"]\n    bar Requests [50, 60]\n    line "Latency" [1, 2]\n'
        blocks = render_mermaid_code(code)

        bar_block = next(b for b in blocks if b.chart["type"] == "bar")
        assert bar_block.chart["series"][0]["name"] == "Requests"

        line_block = next(b for b in blocks if b.chart["type"] == "line")
        assert line_block.chart["series"][0]["name"] == "Latency"

    def test_truncates_to_slack_limits_and_notes_it(self):
        many_categories = ", ".join(f'"c{i}"' for i in range(25))
        many_values = ", ".join(str(i) for i in range(25))
        code = f'xychart\n    x-axis [{many_categories}]\n    bar "series" [{many_values}]\n'
        blocks = render_mermaid_code(code)
        assert len(blocks[0].chart["axis_config"]["categories"]) == 20
        assert len(blocks[0].chart["series"][0]["data"]) == 20

        note = next(b for b in blocks if isinstance(b, ContextBlock))
        assert "20 of 25 data points" in note.text
        assert not any(isinstance(b, LinksBlock) for b in blocks)

    def test_truncation_note_includes_link_when_view_url_given(self):
        many_categories = ", ".join(f'"c{i}"' for i in range(25))
        many_values = ", ".join(str(i) for i in range(25))
        code = f'xychart\n    x-axis [{many_categories}]\n    bar "series" [{many_values}]\n'
        blocks = render_mermaid_code(code, view_url="https://app.example.com/chart")

        link_block = next(b for b in blocks if isinstance(b, LinksBlock))
        assert link_block.links[0].text == "View all Data"
        assert link_block.links[0].url == "https://app.example.com/chart"

    def test_series_count_truncation_is_noted(self):
        bar_lines = "\n".join(f'    bar "series-{i}" [{i}]' for i in range(15))
        code = f"xychart\n{bar_lines}\n"
        blocks = render_mermaid_code(code)

        chart_block = next(b for b in blocks if isinstance(b, ChartBlock))
        assert len(chart_block.chart["series"]) == 12

        note = next(b for b in blocks if isinstance(b, ContextBlock))
        assert "12 of 15 series" in note.text

    def test_no_truncation_note_when_within_limits(self):
        blocks = render_mermaid_code(XYCHART)
        assert not any(isinstance(b, ContextBlock) for b in blocks)


class TestRenderPie:
    def test_renders_pie_chart_block_sorted_descending(self):
        blocks = render_mermaid_code(PIE)

        assert len(blocks) == 1
        assert isinstance(blocks[0], ChartBlock)
        assert blocks[0].title == "Resource Usage"
        assert blocks[0].chart["type"] == "pie"
        assert blocks[0].chart["segments"] == [
            {"label": "CPU", "value": 45.0},
            {"label": "Memory", "value": 30.0},
            {"label": "Disk", "value": 25.0},
        ]

    def test_over_limit_segments_rolled_into_other(self):
        # 15 slices valued 15, 14, ..., 1 (descending). Top 11 survive as-is;
        # the smallest 4 (4+3+2+1=10) should be combined into "Other".
        slice_lines = "\n".join(f'    "s{i}" : {15 - i}' for i in range(15))
        code = f"pie\n{slice_lines}\n"
        blocks = render_mermaid_code(code)

        chart_block = next(b for b in blocks if isinstance(b, ChartBlock))
        segments = chart_block.chart["segments"]
        assert len(segments) == 12
        assert segments[:11] == [{"label": f"s{i}", "value": float(15 - i)} for i in range(11)]
        assert segments[11] == {"label": "Other", "value": 10.0}
        # The total across shown segments still equals the true original total.
        assert sum(s["value"] for s in segments) == sum(15 - i for i in range(15))

        note = next(b for b in blocks if isinstance(b, ContextBlock))
        assert '4 smallest segments combined into "Other"' in note.text

    def test_unquoted_title_is_extracted(self):
        # Real production case: some agents don't quote the pie title, unlike
        # the documented `pie title "..."` format.
        code = 'pie title CPU Usage by Namespace (millicores)\n    "actions-runner-system-1" : 5694\n'
        blocks = render_mermaid_code(code)

        assert isinstance(blocks[0], ChartBlock)
        assert blocks[0].title == "CPU Usage by Namespace (millicores)"

    def test_unquoted_slice_label_is_parsed(self):
        # Mermaid also allows an unquoted slice label with no spaces, same
        # convention title/y-axis/series already support - without this, an
        # unquoted slice silently vanished instead of erroring, since the
        # remaining quoted slices alone still made a "successful" pie chart.
        code = 'pie\n    CPU : 45\n    "Memory" : 30\n    Disk : 25\n'
        blocks = render_mermaid_code(code)

        assert isinstance(blocks[0], ChartBlock)
        assert blocks[0].chart["segments"] == [
            {"label": "CPU", "value": 45.0},
            {"label": "Memory", "value": 30.0},
            {"label": "Disk", "value": 25.0},
        ]

    def test_untagged_fence_with_unquoted_title_end_to_end(self):
        # Exact shape of a real failing message: bare ``` fence, unquoted title.
        text = (
            "Here is the pie chart.\n\n"
            "```\n"
            "pie title CPU Usage by Namespace (millicores)\n"
            '    "actions-runner-system-1" : 5694\n'
            '    "nudgebee" : 1978\n'
            "```\n\n"
            "Let me know if you need more detail."
        )
        segments = split_mermaid_segments(text)
        assert [s.is_mermaid for s in segments] == [False, True, False]

        blocks = render_mermaid_code(segments[1].text)
        assert isinstance(blocks[0], ChartBlock)
        assert blocks[0].title == "CPU Usage by Namespace (millicores)"
        assert blocks[0].chart["segments"] == [
            # "actions-runner-system-1" (24 chars) exceeds the 20-char label cap.
            {"label": "actions-runner-syst…", "value": 5694.0},
            {"label": "nudgebee", "value": 1978.0},
        ]


class TestRenderFallback:
    def test_flowchart_falls_back_to_labeled_code_block(self):
        blocks = render_mermaid_code(FLOWCHART)

        assert isinstance(blocks[0], ContextBlock)
        assert "Flowchart" in blocks[0].text

        assert isinstance(blocks[1], MarkdownBlock)
        assert "API Gateway" in blocks[1].text

        assert not any(isinstance(b, LinksBlock) for b in blocks)

    def test_large_flowchart_truncation_is_noted(self):
        # MarkdownBlock hard-truncates the fenced code at BLOCK_SIZE_LIMIT
        # with no indication - unlike the chart/table paths, which always
        # call out truncation explicitly. A large diagram must get the same
        # visible note instead of silently losing its closing fence.
        nodes = "\n".join(f'    N{i}["Node {i}"] --> N{i + 1}["Node {i + 1}"]' for i in range(200))
        code = f"flowchart TD\n{nodes}\n"
        blocks = render_mermaid_code(code)

        notes = [b.text for b in blocks if isinstance(b, ContextBlock)]
        assert any("truncated" in n for n in notes)

    def test_flowchart_with_view_url_adds_link_block(self):
        blocks = render_mermaid_code(FLOWCHART, view_url="https://app.example.com/ask-nudgebee?accountId=1")

        link_block = next(b for b in blocks if isinstance(b, LinksBlock))
        assert link_block.links[0].text == "View all Data"
        assert link_block.links[0].url == "https://app.example.com/ask-nudgebee?accountId=1"

    def test_malformed_xychart_without_series_falls_back(self):
        blocks = render_mermaid_code('xychart\n    title "Empty"\n')
        assert isinstance(blocks[0], ContextBlock)

    def test_unknown_diagram_type_labeled_generic(self):
        blocks = render_mermaid_code("stateDiagram-v2\n    [*] --> Active\n")
        assert isinstance(blocks[0], ContextBlock)
        assert "State diagram" in blocks[0].text
