"""
Tests for To/Cc/Bcc header hygiene on the email generator.

The contract being defended: Bcc addresses must NEVER appear in MIME headers.
They participate in the SMTP envelope (RCPT TO) only — disclosing them in
headers would defeat the purpose of "blind" carbon copy.
"""

from notifications_server.emailer import build_envelope_recipients, generate_email
from notifications_server.emailer.sender import _envelope_from_message


def _make_msg(**kwargs):
    return generate_email(
        subject="hello",
        template_params={"message": "body"},
        template_type="generic",
        **kwargs,
    )


def test_bcc_never_appears_in_headers():
    msg = _make_msg(to=["a@x.com"], cc=["b@x.com"], bcc=["secret@y.com", "also@y.com"])

    assert msg["Bcc"] is None, "Bcc header must not be set on the MIME message"
    serialized = msg.as_string()
    assert "secret@y.com" not in serialized
    assert "also@y.com" not in serialized
    assert "Bcc:" not in serialized
    assert "bcc:" not in serialized


def test_to_header_renders_list_as_comma_separated():
    msg = _make_msg(to=["a@x.com", "b@x.com"])
    assert msg["To"] == "a@x.com, b@x.com"


def test_to_header_accepts_single_string():
    msg = _make_msg(to="solo@x.com")
    assert msg["To"] == "solo@x.com"


def test_cc_header_is_set_when_provided():
    msg = _make_msg(to=["a@x.com"], cc=["c1@x.com", "c2@x.com"])
    assert msg["Cc"] == "c1@x.com, c2@x.com"


def test_cc_header_is_absent_when_empty():
    msg = _make_msg(to=["a@x.com"])
    assert msg["Cc"] is None


def test_envelope_includes_all_three_groups():
    envelope = build_envelope_recipients(
        ["a@x.com", "b@x.com"],
        ["c@x.com"],
        ["d@x.com", "e@x.com"],
    )
    assert envelope == ["a@x.com", "b@x.com", "c@x.com", "d@x.com", "e@x.com"]


def test_envelope_handles_missing_cc_and_bcc():
    envelope = build_envelope_recipients(["only@x.com"])
    assert envelope == ["only@x.com"]


def test_envelope_normalizes_strings_to_lists():
    envelope = build_envelope_recipients("a@x.com", "b@x.com", "c@x.com")
    assert envelope == ["a@x.com", "b@x.com", "c@x.com"]


def test_envelope_from_message_parses_multi_address_to_header():
    msg = _make_msg(to=["a@x.com", "b@x.com"])
    assert _envelope_from_message(msg) == ["a@x.com", "b@x.com"]


def test_envelope_from_message_includes_cc_when_no_explicit_envelope():
    msg = _make_msg(to=["a@x.com"], cc=["c@x.com", "d@x.com"])
    assert _envelope_from_message(msg) == ["a@x.com", "c@x.com", "d@x.com"]


def test_envelope_from_message_returns_single_to_address():
    msg = _make_msg(to="solo@x.com")
    assert _envelope_from_message(msg) == ["solo@x.com"]
