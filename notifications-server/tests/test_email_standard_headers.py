"""
Tests that every outgoing email carries Date and Message-ID headers, whichever path sends it.
"""

import socket
from email import message_from_string
from email.mime.multipart import MIMEMultipart
from email.utils import parsedate_to_datetime
from unittest.mock import MagicMock

import pytest

from notifications_server.configs.settings import settings
from notifications_server.emailer import sender


@pytest.fixture
def smtp(monkeypatch):
    for key, value in {
        "server_host": "smtp.example.com",
        "server_port": 25,
        "server_user": "",
        "server_password": "",
        "from_address": "alerts@example.com",
        "server_tls": "none",
    }.items():
        monkeypatch.setattr(settings.email, key, value)
    plain = MagicMock(name="SMTP")
    monkeypatch.setattr(sender.smtplib, "SMTP", plain)
    return plain.return_value


def _message(frm="Alerts <alerts@example.com>", to="user@example.com"):
    msg = MIMEMultipart()
    if frm is not None:
        msg["From"] = frm
    msg["To"] = to
    msg["Subject"] = "hello"
    return msg


def _sent(conn, call=0):
    return message_from_string(conn.sendmail.call_args_list[call].args[2])


def test_send_adds_date_and_message_id(smtp):
    sender.send_email(_message(), envelope_recipients=["user@example.com"])

    sent = _sent(smtp)
    assert parsedate_to_datetime(sent["Date"]).tzinfo is not None
    assert sent["Message-ID"].startswith("<") and sent["Message-ID"].endswith("@example.com>")


def test_existing_headers_are_kept(smtp):
    msg = _message()
    msg["Date"] = "Tue, 01 Sep 2026 10:00:00 +0000"
    msg["Message-ID"] = "<fixed@example.com>"

    sender.send_email(msg, envelope_recipients=["user@example.com"])

    sent = _sent(smtp)
    assert sent.get_all("Date") == ["Tue, 01 Sep 2026 10:00:00 +0000"]
    assert sent.get_all("Message-ID") == ["<fixed@example.com>"]


def test_batch_gives_each_message_its_own_id(smtp):
    sender.send_email_batch([_message(to="a@example.com"), _message(to="b@example.com")])

    ids = [_sent(smtp, i)["Message-ID"] for i in range(2)]
    assert all(_sent(smtp, i)["Date"] for i in range(2))
    assert len(set(ids)) == 2


def test_sendmail_fallback_also_gets_headers(monkeypatch):
    monkeypatch.setattr(settings.email, "server_host", "")
    captured = []
    monkeypatch.setattr(sender, "_send_email_from_default_service", lambda message, rcpt=None: captured.append(message))

    sender.send_email(_message(), envelope_recipients=["user@example.com"])

    assert captured[0]["Date"] and captured[0]["Message-ID"]


def test_message_id_never_looks_up_the_hostname(smtp, monkeypatch):
    def fail(*_):
        raise AssertionError("hostname lookup")

    monkeypatch.setattr(socket, "getfqdn", fail)

    sender.send_email(_message(frm=None), envelope_recipients=["user@example.com"])

    assert _sent(smtp)["Message-ID"].endswith("@localhost>")
