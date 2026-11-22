from notifications_server.message_templates.blocks import BLOCK_SIZE_LIMIT
from notifications_server.utils.code_fence import render_code_fence, split_code_fence_segments


class TestSplitCodeFenceSegments:
    def test_no_fence_returns_single_text_segment(self):
        segments = split_code_fence_segments("just plain text")
        assert len(segments) == 1
        assert segments[0].is_code_fence is False
        assert segments[0].text == "just plain text"

    def test_extracts_tagged_fence_between_text(self):
        text = "before\n```yaml\nkey: value\n```\nafter"
        segments = split_code_fence_segments(text)
        assert [s.is_code_fence for s in segments] == [False, True, False]
        assert segments[1].text == "key: value"

    def test_extracts_bare_fence(self):
        text = "before\n```\nraw output\n```\nafter"
        segments = split_code_fence_segments(text)
        assert [s.is_code_fence for s in segments] == [False, True, False]
        assert segments[1].text == "raw output"

    def test_unterminated_fence_in_source_text_is_left_as_plain_text(self):
        text = "before\n```yaml\nkey: value\nno closing fence"
        segments = split_code_fence_segments(text)
        assert len(segments) == 1
        assert segments[0].is_code_fence is False

    def test_long_multiline_fence_stays_one_segment(self):
        body = "\n".join(f"config_item_{i:03d}: value-{i:03d}" for i in range(108))
        text = f"Here is the manifest:\n\n```yaml\n{body}\n```\n\nDone."
        segments = split_code_fence_segments(text)
        assert [s.is_code_fence for s in segments] == [False, True, False]
        assert segments[1].text == body


class TestRenderCodeFence:
    def test_renders_one_bare_markdown_block(self):
        # Slack's mrkdwn doesn't recognize a language tag as a fence opener.
        blocks = render_code_fence("key: value")
        assert len(blocks) == 1
        assert blocks[0].text == "```\nkey: value\n```"

    def test_short_code_has_no_truncation_notice(self):
        blocks = render_code_fence("short")
        assert len(blocks) == 1

    def test_oversized_code_stays_a_valid_closed_fence_with_a_notice(self):
        # MarkdownBlock's own hard truncation would slice off the closing fence instead.
        huge = "x" * (BLOCK_SIZE_LIMIT + 500)
        blocks = render_code_fence(huge)
        assert len(blocks) == 2
        assert len(blocks[0].text) < BLOCK_SIZE_LIMIT
        assert blocks[0].text.startswith("```\n")
        assert blocks[0].text.endswith("...\n```")
        assert "truncated" in blocks[1].text
