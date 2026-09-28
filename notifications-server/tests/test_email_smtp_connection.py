"""
Tests for SMTP connection setup: TLS mode selection, optional authentication,
and From header / envelope sender handling.
"""

import socket
import smtplib
import threading
from email.mime.multipart import MIMEMultipart
from unittest.mock import MagicMock

import pytest

from notifications_server.configs.settings import EmailSettings, settings
from notifications_server.emailer import format_from_header, generate_email
from notifications_server.emailer import sender


@pytest.fixture
def smtp_mocks(monkeypatch):
    plain = MagicMock(name="SMTP")
    ssl_ = MagicMock(name="SMTP_SSL")
    monkeypatch.setattr(sender.smtplib, "SMTP", plain)
    monkeypatch.setattr(sender.smtplib, "SMTP_SSL", ssl_)
    return plain, ssl_


@pytest.fixture
def email_config(monkeypatch):
    def _apply(**values):
        defaults = {
            "server_host": "smtp.example.com",
            "server_port": 25,
            "server_user": "",
            "server_password": "",
            "from_address": "alerts@example.com",
            "from_name": "",
            "server_tls": "none",
        }
        defaults.update(values)
        for key, value in defaults.items():
            monkeypatch.setattr(settings.email, key, value)

    return _apply


def _message():
    msg = MIMEMultipart()
    msg["To"] = "user@example.com"
    msg["Subject"] = "hello"
    return msg


@pytest.mark.parametrize(
    "mode, port, expected",
    [
        ("auto", "465", "ssl"),
        ("auto", "587", "starttls"),
        ("auto", "25", None),
        ("none", "25", "none"),
        ("NONE", "25", "none"),
        (" starttls ", "2525", "starttls"),
        ("ssl", "25", "ssl"),
        ("", "465", "ssl"),
        ("plaintext", "25", None),
    ],
)
def test_resolve_tls_mode(monkeypatch, mode, port, expected):
    monkeypatch.setattr(settings.email, "server_tls", mode)
    assert sender._resolve_tls_mode(port) == expected


def test_none_mode_opens_plain_connection_without_starttls(email_config, smtp_mocks):
    email_config(server_tls="none")
    plain, ssl_ = smtp_mocks

    conn = sender._create_smtp_connection("smtp.example.com", "25", context=None)

    plain.assert_called_once_with("smtp.example.com", "25", timeout=sender.SMTP_TIMEOUT_SECONDS)
    assert conn is plain.return_value
    conn.starttls.assert_not_called()
    ssl_.assert_not_called()


def test_auto_mode_keeps_existing_port_behaviour(email_config, smtp_mocks):
    email_config(server_tls="auto")
    plain, ssl_ = smtp_mocks

    sender._create_smtp_connection("smtp.example.com", "465", context="ctx")
    ssl_.assert_called_once_with("smtp.example.com", "465", context="ctx", timeout=sender.SMTP_TIMEOUT_SECONDS)

    conn = sender._create_smtp_connection("smtp.example.com", "587", context="ctx")
    plain.assert_called_once_with("smtp.example.com", "587", timeout=sender.SMTP_TIMEOUT_SECONDS)
    conn.starttls.assert_called_once_with(context="ctx")


def test_auto_mode_on_unknown_port_opens_no_connection(email_config, smtp_mocks):
    email_config(server_tls="auto")
    plain, ssl_ = smtp_mocks

    assert sender._create_smtp_connection("smtp.example.com", "25", context=None) is None
    plain.assert_not_called()
    ssl_.assert_not_called()


@pytest.fixture
def silent_server():
    """A TCP listener that accepts connections but never sends an SMTP greeting."""
    srv = socket.socket()
    srv.bind(("127.0.0.1", 0))
    srv.listen(5)
    accepted = []
    threading.Thread(target=lambda: accepted.append(srv.accept()), daemon=True).start()
    yield srv.getsockname()[1]
    for conn, _ in accepted:
        conn.close()
    srv.close()


def test_unresponsive_server_times_out_instead_of_hanging(email_config, monkeypatch, silent_server):
    email_config(server_tls="none")
    monkeypatch.setattr(sender, "SMTP_TIMEOUT_SECONDS", 1)
    outcome = {}

    def connect():
        try:
            sender._create_smtp_connection("127.0.0.1", str(silent_server), context=None)
        except Exception as e:
            outcome["error"] = e

    worker = threading.Thread(target=connect, daemon=True)
    worker.start()
    worker.join(timeout=10)

    assert not worker.is_alive(), "SMTP connection blocked with no timeout"
    assert isinstance(outcome.get("error"), (smtplib.SMTPServerDisconnected, socket.timeout))


def test_send_without_credentials_skips_login(email_config, smtp_mocks):
    email_config()
    plain, _ = smtp_mocks

    sender.send_email(_message(), envelope_recipients=["user@example.com"])

    conn = plain.return_value
    conn.login.assert_not_called()
    conn.sendmail.assert_called_once()
    assert conn.sendmail.call_args.args[:2] == ("alerts@example.com", ["user@example.com"])


def test_send_with_credentials_still_logs_in(email_config, smtp_mocks):
    email_config(server_port=587, server_tls="auto", server_user="bot@example.com", server_password="secret")
    plain, _ = smtp_mocks

    sender.send_email(_message(), envelope_recipients=["user@example.com"])

    plain.return_value.login.assert_called_once_with("bot@example.com", "secret")


def test_batch_send_without_credentials_skips_login(email_config, smtp_mocks):
    email_config()
    plain, _ = smtp_mocks

    sender.send_email_batch([_message(), _message()])

    conn = plain.return_value
    conn.login.assert_not_called()
    assert conn.sendmail.call_count == 2


def test_envelope_sender_is_bare_address_when_from_has_display_name(email_config, smtp_mocks):
    email_config(from_address="Alerts <alerts@example.com>")
    plain, _ = smtp_mocks

    sender.send_email(_message(), envelope_recipients=["user@example.com"])

    assert plain.return_value.sendmail.call_args.args[0] == "alerts@example.com"


def _email_settings(host="", from_address=""):
    return EmailSettings(
        _env_file=None,
        email_server_host=host,
        email_server_port=465,
        email_server_user="",
        email_server_password="",
        email_from=from_address,
    )


def test_is_configured_without_credentials():
    cfg = _email_settings(host="smtp.example.com", from_address="alerts@example.com")
    assert cfg.is_configured
    assert cfg.get_smtp_params() == ["465", "smtp.example.com", "", "", "alerts@example.com"]


def test_is_configured_requires_host_and_from():
    assert not _email_settings(host="smtp.example.com").is_configured
    assert not _email_settings(from_address="alerts@example.com").is_configured


def test_from_header_uses_display_name(monkeypatch):
    monkeypatch.setattr(settings.email, "from_name", "Nudgebee Alerts")
    assert format_from_header("alerts@example.com") == "Nudgebee Alerts <alerts@example.com>"
    assert format_from_header("Other <alerts@example.com>") == "Nudgebee Alerts <alerts@example.com>"


def test_from_header_unchanged_without_display_name(monkeypatch):
    monkeypatch.setattr(settings.email, "from_name", "")
    assert format_from_header("alerts@example.com") == "alerts@example.com"


def test_generated_email_carries_display_name(monkeypatch):
    monkeypatch.setattr(settings.email, "from_name", "Nudgebee Alerts")
    msg = generate_email(
        to=["user@example.com"],
        subject="hello",
        template_params={"message": "body"},
        template_type="generic",
        frm="alerts@example.com",
    )
    assert msg["From"] == "Nudgebee Alerts <alerts@example.com>"
