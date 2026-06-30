from notifications_server.utils.transformer import Transformer


class TestApplyLengthLimit:
    def test_under_limit_returns_unchanged(self):
        assert Transformer.apply_length_limit("short", 10) == "short"

    def test_exactly_at_limit_returns_unchanged(self):
        assert Transformer.apply_length_limit("1234567890", 10) == "1234567890"

    def test_default_truncator(self):
        # docstring example: 9-char limit -> msg[:6] + "..."
        assert Transformer.apply_length_limit("1234567890", 9) == "123456..."

    def test_custom_truncator(self):
        assert Transformer.apply_length_limit("1234567890", 9, ".") == "12345678."


class TestGetMarkdownLinks:
    def test_extracts_slack_links(self):
        text = "see <http://x.com|link> and <http://y.com|other>"
        assert Transformer.get_markdown_links(text) == ["<http://x.com|link>", "<http://y.com|other>"]

    def test_no_links_returns_empty(self):
        assert Transformer.get_markdown_links("plain text, no links") == []


class TestToGithubMarkdown:
    def test_link_with_angular_brackets(self):
        assert Transformer.to_github_markdown("<http://example.com|click>") == "[click](<http://example.com>)"

    def test_link_without_angular_brackets(self):
        assert (
            Transformer.to_github_markdown("<http://example.com|click>", add_angular_brackets=False)
            == "[click](http://example.com)"
        )

    def test_bold_conversion(self):
        assert Transformer.to_github_markdown("*bold*") == "**bold**"


class TestToSlackMarkdownLink:
    def test_link_conversion(self):
        assert Transformer.to_slack_markdown_link("[click](http://example.com)") == "<http://example.com|click>"

    def test_bold_conversion(self):
        assert Transformer.to_slack_markdown_link("**bold**") == "*bold*"


class TestSlackMarkdownToGenericMarkdown:
    def test_empty_input_returns_input(self):
        assert Transformer.slack_markdown_to_generic_markdown("") == ""

    def test_link_conversion(self):
        assert Transformer.slack_markdown_to_generic_markdown("<http://x.com|text>") == "[text](http://x.com)"

    def test_bold_italic_strikethrough(self):
        assert Transformer.slack_markdown_to_generic_markdown("*b*") == "**b**"
        assert Transformer.slack_markdown_to_generic_markdown("_i_") == "*i*"
        assert Transformer.slack_markdown_to_generic_markdown("~s~") == "~~s~~"

    def test_special_mentions(self):
        assert Transformer.slack_markdown_to_generic_markdown("<!channel>") == "@channel"
        assert Transformer.slack_markdown_to_generic_markdown("<!here>") == "@here"

    def test_bare_url_conversion(self):
        assert (
            Transformer.slack_markdown_to_generic_markdown("<http://example.com>")
            == "[http://example.com](http://example.com)"
        )

    def test_none_input_returns_none(self):
        assert Transformer.slack_markdown_to_generic_markdown(None) is None


class TestSmartTruncateJson:
    def test_primitive_value(self):
        assert Transformer.smart_truncate_json(42) == "42"

    def test_non_json_string_falls_back_verbatim(self):
        assert Transformer.smart_truncate_json("not json") == "not json"

    def test_dict_returns_serialized_json(self):
        assert Transformer.smart_truncate_json({"key": "value"}) == '{"key": "value"}'

    def test_dict_truncation(self):
        assert Transformer.smart_truncate_json({"a": 1, "b": 2}, max_length=60) == '{"a": 1, ... 1 more fields}'

    def test_list_serialization_and_truncation(self):
        assert Transformer.smart_truncate_json([1, 2, 3]) == "[1, 2, 3]"
        assert Transformer.smart_truncate_json([1, 2, 3, 4, 5, 6]) == "[1, 2, 3, 4, 5... 1 more items]"

    def test_valid_json_string_is_parsed_then_serialized(self):
        assert Transformer.smart_truncate_json('{"key": "value"}') == '{"key": "value"}'
