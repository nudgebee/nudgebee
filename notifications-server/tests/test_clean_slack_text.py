"""Tests for Events.clean_slack_text.

What these pin:
  * a bare URL and a piped/labelled URL both survive, label discarded
  * @mentions, #channel refs and !here are stripped (captured separately
    by _stash_thread_mentions before this runs)
  * mailto: links keep the address
  * an HTML-escaped query string (Slack's escaping of a literal &) is
    unescaped so the URL reaching the LLM is well-formed
  * None input doesn't raise
"""

from notifications_server.services.events import Events


def test_plain_url_survives():
    assert Events.clean_slack_text("check <https://example.com/pull/1234>") == "check https://example.com/pull/1234"


def test_labelled_url_keeps_url_drops_label():
    assert (
        Events.clean_slack_text("check <https://example.com/pull/1234|this PR>")
        == "check https://example.com/pull/1234"
    )


def test_mention_is_stripped():
    assert Events.clean_slack_text("hi <@U123> how are you") == "hi  how are you"


def test_channel_ref_is_stripped():
    assert Events.clean_slack_text("see <#C123|general> for details") == "see  for details"


def test_here_is_stripped():
    assert Events.clean_slack_text("<!here> please review") == "please review"


def test_mailto_keeps_address():
    assert Events.clean_slack_text("email <mailto:a@b.com|a@b.com>") == "email a@b.com"


def test_escaped_query_string_is_unescaped():
    assert (
        Events.clean_slack_text("open <https://grafana.io/d/abc?from=now-1h&amp;to=now&amp;var=x>")
        == "open https://grafana.io/d/abc?from=now-1h&to=now&var=x"
    )


def test_none_input_returns_empty_string():
    assert Events.clean_slack_text(None) == ""
