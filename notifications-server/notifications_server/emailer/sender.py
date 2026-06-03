import asyncio
import logging
import smtplib
import ssl
import subprocess
from concurrent.futures import ThreadPoolExecutor
from email.utils import getaddresses

from notifications_server.configs.settings import get_smtp_params, settings
from notifications_server.utils.encode_utils import is_valid_port

LOG = logging.getLogger(__name__)


def _envelope_from_message(message):
    """Build a RCPT TO list from a message's To and Cc headers.

    Used as the backward-compatible fallback when callers don't supply an
    explicit envelope. `getaddresses` correctly handles single addresses,
    comma-separated multi-address headers, and display-name forms — avoiding
    the trap of passing a raw header string (e.g. "a@x, b@x") as a single
    recipient to smtplib.
    """
    header_values = [message.get(name) for name in ("To", "Cc") if message.get(name)]
    return [addr for _, addr in getaddresses(header_values) if addr]


def send_email(message, envelope_recipients=None):
    """Send a single email message.

    `envelope_recipients` is the explicit SMTP RCPT TO list (To + Cc + Bcc).
    When None, the recipients are derived from the To and Cc headers — note
    that Bcc cannot be recovered this way (it is intentionally not in
    headers), so combined Cc/Bcc senders must always pass `envelope_recipients`.
    """
    rcpt = envelope_recipients if envelope_recipients else _envelope_from_message(message)
    smtp_params = get_smtp_params()
    if smtp_params is not None:
        if _is_valid_smtp_params(smtp_params):
            port, server, email, password, from_email = smtp_params
            _send_email_to_user_smtp(server, port, email, password, message, from_email, rcpt)
            return
        else:
            LOG.warning("User SMTP parameters are not valid")
    _send_email_from_default_service(message, rcpt)


def _is_valid_smtp_params(params):
    if params is None:
        return False
    port, server, email, password, _ = params
    for value in (server, email, password):
        if value is None:
            LOG.exception("Server/email/password should not be none")
            return False
    if not isinstance(server, str):
        LOG.exception("server %s is not instance ", server)
        return False
    if not is_valid_port(port):
        LOG.exception("port %s is not valid ", port)
    return True


def _send_email_to_user_smtp(server, port, email, password, message, from_email, rcpt):
    context = ssl.create_default_context()
    context.minimum_version = ssl.TLSVersion.TLSv1_2
    try:
        if port == "465":
            with smtplib.SMTP_SSL(server, port, context=context) as smtp_server:
                smtp_server.login(email, password)
                smtp_server.sendmail(from_email, rcpt, message.as_string())
        elif port == "587":
            with smtplib.SMTP(server, port) as smtp_server:
                smtp_server.starttls(context=context)
                smtp_server.login(email, password)
                smtp_server.sendmail(from_email, rcpt, message.as_string())
        else:
            LOG.error(f"Unsupported port: {port}. Use port 465 for SSL or 587 for STARTTLS.")
            return

        LOG.info(f"Email sent successfully. from: {from_email}")

    except Exception as e:
        LOG.exception(f"Could not send mail using server {server} and port {port} with email {email}. Error {str(e)}")


def _send_email_from_default_service(message, rcpt=None):
    sendmail_location = "/usr/sbin/sendmail"
    try:
        if rcpt:
            recipients = rcpt if isinstance(rcpt, list) else [rcpt]
            subprocess.run(
                [sendmail_location, "-i", "--"] + recipients,
                input=message.as_string(),
                text=True,
                timeout=30,
                check=True,
            )
        else:
            subprocess.run(
                [sendmail_location, "-t", "-i"],
                input=message.as_string(),
                text=True,
                timeout=30,
                check=True,
            )
    except subprocess.SubprocessError as e:
        LOG.exception("Failed to send email via sendmail: %s", e)


def send_email_batch(messages):
    """Send multiple emails over a single SMTP connection."""
    if not messages:
        return

    smtp_params = get_smtp_params()
    if smtp_params is not None and _is_valid_smtp_params(smtp_params):
        port, server, email, password, from_email = smtp_params
        ctx = ssl.create_default_context()
        ctx.minimum_version = ssl.TLSVersion.TLSv1_2
        try:
            if port == "465":
                smtp_server = smtplib.SMTP_SSL(server, port, context=ctx)
            elif port == "587":
                smtp_server = smtplib.SMTP(server, port)
                smtp_server.starttls(context=ctx)
            else:
                LOG.error("Unsupported port: %s. Use port 465 for SSL or 587 for STARTTLS.", port)
                return

            with smtp_server:
                smtp_server.login(email, password)
                for message in messages:
                    try:
                        smtp_server.sendmail(from_email, message.get("To"), message.as_string())
                    except Exception as e:
                        LOG.warning("Failed to send email to %s: %s", message.get("To"), e)
            LOG.info("Batch of %d emails sent successfully via SMTP", len(messages))
        except Exception as e:
            LOG.exception("Could not send batch via SMTP %s:%s - %s", server, port, e)
        return

    # Fallback to default sendmail for each message
    for message in messages:
        _send_email_from_default_service(message)


# Thread pool for async email sending
_email_executor = ThreadPoolExecutor(max_workers=settings.email.max_concurrent_sends, thread_name_prefix="email_sender")


async def send_email_async(message, envelope_recipients=None):
    """Async wrapper for send_email using thread pool"""
    loop = asyncio.get_running_loop()
    await loop.run_in_executor(_email_executor, send_email, message, envelope_recipients)


async def send_email_batch_async(messages):
    """Async wrapper for send_email_batch using thread pool"""
    loop = asyncio.get_running_loop()
    await loop.run_in_executor(_email_executor, send_email_batch, messages)
