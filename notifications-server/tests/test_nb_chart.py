import json

from notifications_server.message_templates.blocks import ChartBlock, ContextBlock, LinksBlock
from notifications_server.utils.nb_chart import render_nb_chart, split_nb_chart_segments

BAR_CHART = (
    '{"type":"bar","title":"Provisioned vs Used (Gi)","labels":["storage-loki-0","elasticsearch-data-0"],'
    '"series":[{"key":"Provisioned","data":[100,105]},{"key":"Used","data":[3.4,23.2]}],"format":"gi"}'
)

PIE_CHART = (
    '{"type":"pie","title":"Cost by Service","labels":["EC2","RDS","S3"],' '"values":[1200,800,150],"format":"usd"}'
)


class TestSplitNbChartSegments:
    def test_no_chart_returns_single_text_segment(self):
        segments = split_nb_chart_segments("just plain text")
        assert len(segments) == 1
        assert segments[0].is_chart is False
        assert segments[0].text == "just plain text"

    def test_extracts_chart_between_text(self):
        text = f"before\n```nb-chart\n{BAR_CHART}\n```\nafter"
        segments = split_nb_chart_segments(text)
        assert [s.is_chart for s in segments] == [False, True, False]
        assert segments[1].text == BAR_CHART

    def test_untagged_fence_with_nb_chart_shaped_json_is_detected(self):
        # Regression: real llm-server output doesn't always include the
        # ```nb-chart tag, unlike what was originally assumed - a bare ```
        # fence around the same JSON shape must still be detected.
        text = f"before\n```\n{BAR_CHART}\n```\nafter"
        segments = split_nb_chart_segments(text)
        assert [s.is_chart for s in segments] == [False, True, False]
        assert segments[1].text == BAR_CHART

    def test_untagged_fence_with_unrelated_json_is_not_an_nb_chart(self):
        text = 'before\n```\n{"foo": "bar"}\n```\nafter'
        segments = split_nb_chart_segments(text)
        assert len(segments) == 1
        assert segments[0].is_chart is False

    def test_untagged_fence_with_non_json_content_is_not_an_nb_chart(self):
        text = 'before\n```\nprint("hello")\n```\nafter'
        segments = split_nb_chart_segments(text)
        assert len(segments) == 1
        assert segments[0].is_chart is False

    def test_fence_with_other_language_tag_is_not_an_nb_chart(self):
        text = f"before\n```json\n{BAR_CHART}\n```\nafter"
        segments = split_nb_chart_segments(text)
        assert len(segments) == 1
        assert segments[0].is_chart is False


class TestRenderNbChart:
    def test_renders_bar_chart_with_multiple_series(self):
        blocks = render_nb_chart(BAR_CHART)

        assert len(blocks) == 1
        assert isinstance(blocks[0], ChartBlock)
        assert blocks[0].title == "Provisioned vs Used (Gi)"
        assert blocks[0].chart["type"] == "bar"
        assert blocks[0].chart["axis_config"]["categories"] == ["storage-loki-0", "elasticsearch-data-0"]
        assert blocks[0].chart["axis_config"]["y_label"] == "GiB"
        assert blocks[0].chart["series"] == [
            {
                "name": "Provisioned",
                "data": [
                    {"label": "storage-loki-0", "value": 100.0},
                    {"label": "elasticsearch-data-0", "value": 105.0},
                ],
            },
            {
                "name": "Used",
                "data": [{"label": "storage-loki-0", "value": 3.4}, {"label": "elasticsearch-data-0", "value": 23.2}],
            },
        ]

    def test_renders_pie_chart_from_labels_and_values(self):
        blocks = render_nb_chart(PIE_CHART)

        assert len(blocks) == 1
        assert blocks[0].title == "Cost by Service"
        assert blocks[0].chart["type"] == "pie"
        assert blocks[0].chart["segments"] == [
            {"label": "EC2", "value": 1200.0},
            {"label": "RDS", "value": 800.0},
            {"label": "S3", "value": 150.0},
        ]
        # Pie charts have no axis_config, so there's nowhere to put "format" - skipped.

    def test_doughnut_renders_as_pie(self):
        code = '{"type":"doughnut","title":"Split","labels":["A","B"],"values":[1,2]}'
        blocks = render_nb_chart(code)
        assert blocks[0].chart["type"] == "pie"

    def test_area_and_line_types_pass_through(self):
        for chart_type in ("area", "line"):
            code = f'{{"type":"{chart_type}","title":"T","labels":["A","B"],"series":[{{"key":"k","data":[1,2]}}]}}'
            blocks = render_nb_chart(code)
            assert blocks[0].chart["type"] == chart_type

    def test_format_number_is_not_shown_as_y_label(self):
        code = '{"type":"bar","title":"T","labels":["A"],"series":[{"key":"k","data":[1]}],"format":"number"}'
        blocks = render_nb_chart(code)
        assert "y_label" not in blocks[0].chart["axis_config"]

    def test_unknown_chart_type_returns_empty(self):
        code = '{"type":"scatter","title":"T","labels":["A"],"series":[{"key":"k","data":[1]}]}'
        assert render_nb_chart(code) == []

    def test_invalid_json_returns_empty(self):
        assert render_nb_chart("not json") == []

    def test_missing_labels_returns_empty(self):
        code = '{"type":"bar","title":"T","series":[{"key":"k","data":[1]}]}'
        assert render_nb_chart(code) == []

    def test_pie_without_values_returns_empty(self):
        code = '{"type":"pie","title":"T","labels":["A","B"]}'
        assert render_nb_chart(code) == []

    def test_pie_with_label_value_objects_and_no_top_level_labels(self):
        # Real observed shape: the model omits the top-level "labels" array
        # and instead nests each label/value pair inside "values".
        code = (
            '{"type":"doughnut","title":"Spend by Namespace",'
            '"values":[{"label":"nudgebee","value":124.4},{"label":"kube-system","value":73.59}],'
            '"format":"usd"}'
        )
        blocks = render_nb_chart(code)
        assert len(blocks) == 1
        assert blocks[0].chart["type"] == "pie"
        assert blocks[0].chart["segments"] == [
            {"label": "nudgebee", "value": 124.4},
            {"label": "kube-system", "value": 73.59},
        ]

    def test_pie_with_label_value_objects_over_limit_rolls_into_other(self):
        # The "Other" rollup (over 12 slices) must still apply to this
        # shape, same as the parallel labels/values array shape.
        values = [{"label": f"s{i}", "value": float(15 - i)} for i in range(15)]
        code = '{"type":"pie","title":"T","values":' + json.dumps(values) + "}"
        blocks = render_nb_chart(code)

        segments = blocks[0].chart["segments"]
        assert len(segments) == 12
        assert segments[11] == {"label": "Other", "value": 10.0}
        assert sum(s["value"] for s in segments) == sum(15 - i for i in range(15))

    def test_pie_with_labels_array_present_takes_priority_over_label_value_objects(self):
        # If a real top-level "labels" array is present, it must still win -
        # the label/value-object path is a fallback for when "labels" is
        # missing entirely, not an alternative source of truth to merge with.
        code = '{"type":"pie","title":"T","labels":["Real"],' '"values":[{"label":"Ignored","value":1}]}'
        assert render_nb_chart(code) == []  # zip(["Real"], [{"label":...}]) -> float() on a dict fails

    def test_pie_with_malformed_label_value_object_returns_empty(self):
        # All-or-nothing, matching the existing parallel-array shape's
        # behavior: one bad entry fails the whole chart rather than
        # silently dropping just that slice.
        code = '{"type":"pie","title":"T","values":[{"label":"A","value":1},{"label":"B"}]}'
        assert render_nb_chart(code) == []

    def test_pie_with_neither_labels_nor_label_value_objects_returns_empty(self):
        code = '{"type":"pie","title":"T","values":[1,2,3]}'
        assert render_nb_chart(code) == []

    def test_real_untagged_finops_payload_end_to_end(self):
        # Exact shape of a real failing message (captured via debugger): a
        # GFM table, a bare ``` fence (no "nb-chart" tag) around chart JSON,
        # and prose - all in one response.
        text = (
            "| Service | Current 30d ($) | Previous 30d ($) |\n|---|---|---|\n"
            "| Kubernetes [spend_summary] | $1,566.10 | $388.81 |\n\n"
            "```\n"
            '{"type":"bar","title":"Spend: Current vs Previous 30 Days","labels":["Kubernetes"],'
            '"series":[{"key":"Current 30d","data":[1566.10]},{"key":"Previous 30d","data":[388.81]}],'
            '"format":"usd"}\n'
            "```\n\n"
            "Your total cloud spend over the last 30 days is *$1,566.10*."
        )
        segments = split_nb_chart_segments(text)
        assert [s.is_chart for s in segments] == [False, True, False]

        blocks = render_nb_chart(segments[1].text)
        assert isinstance(blocks[0], ChartBlock)
        assert blocks[0].title == "Spend: Current vs Previous 30 Days"
        assert blocks[0].chart["type"] == "bar"

    def test_caps_labels_and_series_to_slack_limits(self):
        labels = [f'"c{i}"' for i in range(25)]
        data = list(range(25))
        code = (
            '{"type":"bar","title":"T","labels":[' + ",".join(labels) + "],"
            '"series":[' + ",".join(f'{{"key":"s{i}","data":{data}}}' for i in range(15)) + "]}"
        )
        blocks = render_nb_chart(code)
        assert len(blocks[0].chart["axis_config"]["categories"]) == 20
        assert len(blocks[0].chart["series"]) == 12
        assert len(blocks[0].chart["series"][0]["data"]) == 20

        note = next(b for b in blocks if isinstance(b, ContextBlock))
        assert "12 of 15 series" in note.text
        assert "20 of 25 data points" in note.text
        assert not any(isinstance(b, LinksBlock) for b in blocks)

    def test_truncation_note_includes_link_when_view_url_given(self):
        labels = [f'"c{i}"' for i in range(25)]
        code = (
            '{"type":"bar","title":"T","labels":[' + ",".join(labels) + "],"
            '"series":[{"key":"s","data":' + str(list(range(25))) + "}]}"
        )
        blocks = render_nb_chart(code, view_url="https://app.example.com/chart")

        link_block = next(b for b in blocks if isinstance(b, LinksBlock))
        assert link_block.links[0].text == "View all Data"
        assert link_block.links[0].url == "https://app.example.com/chart"

    def test_no_truncation_note_when_within_limits(self):
        blocks = render_nb_chart(BAR_CHART)
        assert not any(isinstance(b, ContextBlock) for b in blocks)

    def test_over_limit_pie_segments_rolled_into_other(self):
        # 15 slices, labels s0..s14 with values 1..15 (ascending, so label
        # index and value rank are deliberately reversed). The top 11 by
        # VALUE must survive (s14..s4), not the first 11 in listed order -
        # otherwise the biggest slice could be dropped just because of where
        # it fell in the JSON array. The smallest 4 (s0..s3, values 1+2+3+4)
        # get combined into "Other" so percentages still add up to the total.
        labels = [f'"s{i}"' for i in range(15)]
        values = list(range(1, 16))
        code = '{"type":"pie","title":"T","labels":[' + ",".join(labels) + '],"values":' + str(values) + "}"
        blocks = render_nb_chart(code)

        chart_block = next(b for b in blocks if isinstance(b, ChartBlock))
        segments = chart_block.chart["segments"]
        assert len(segments) == 12
        assert segments[:11] == [{"label": f"s{14 - i}", "value": float(15 - i)} for i in range(11)]
        assert segments[11] == {"label": "Other", "value": 10.0}
        assert sum(s["value"] for s in segments) == sum(values)

        note = next(b for b in blocks if isinstance(b, ContextBlock))
        assert '4 smallest segments combined into "Other"' in note.text
