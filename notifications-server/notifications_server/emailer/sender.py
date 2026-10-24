import asyncio
import logging
import smtplib
import ssl
import subprocess
import time
from concurrent.futures import ThreadPoolExecutor
from email.utils import formatdate, getaddresses, make_msgid, parseaddr

from notifications_server.configs.settings import get_smtp_params, settings
from notifications_server.utils.encode_utils import is_valid_port

LOG = logging.getLogger(__name__)


class EmailBatchError(RuntimeError):
    """Some messages of a batch were not sent. failures maps each recipient that
    was not sent to the error its send raised."""

    def __init__(self, message, failures):
        super().__init__(message)
        self.failures = failures


# Default ports used to infer the connection type when EMAIL_SERVER_TLS is "auto".
SMTP_SSL_PORT = "465"
SMTP_STARTTLS_PORT = "587"

TLS_AUTO = "auto"
TLS_SSL = "ssl"
TLS_STARTTLS = "starttls"
TLS_NONE = "none"

# Bounds every blocking socket operation so an unresponsive server can't pin an email worker thread.
SMTP_TIMEOUT_SECONDS = 60

# A batch message the server answers with a temporary (4xx) error, such as a sending-rate
# limit, is sent again after each of these waits.
TRANSIENT_RETRY_DELAYS_SECONDS = (1, 2, 4)


def _resolve_tls_mode(port):
    """Return the connection type for this send, or None when it can't be determined."""
    mode = (settings.email.server_tls or TLS_AUTO).strip().lower()
    if mode in (TLS_SSL, TLS_STARTTLS, TLS_NONE):
        return mode
    if mode != TLS_AUTO:
        return None
    port = str(port)
    if port == SMTP_SSL_PORT:
        return TLS_SSL
    if port == SMTP_STARTTLS_PORT:
        return TLS_STARTTLS
    return None


def _create_smtp_connection(server, port, context):
    """Create an SMTP connection using the configured EMAIL_SERVER_TLS mode.

    Returns a connected ``smtplib.SMTP``/``SMTP_SSL`` instance (with STARTTLS
    already negotiated when requested), or ``None`` when the mode is invalid or
    is "auto" with a port other than 465/587.
    """
    mode = _resolve_tls_mode(port)
    if mode == TLS_SSL:
        return smtplib.SMTP_SSL(server, port, context=context, timeout=SMTP_TIMEOUT_SECONDS)
    if mode == TLS_STARTTLS:
        smtp_server = smtplib.SMTP(server, port, timeout=SMTP_TIMEOUT_SECONDS)
        smtp_server.starttls(context=context)
        return smtp_server
    if mode == TLS_NONE:
        return smtplib.SMTP(server, port, timeout=SMTP_TIMEOUT_SECONDS)
    LOG.error(
        "Cannot determine SMTP connection type for port %s (EMAIL_SERVER_TLS=%s). "
        "Set EMAIL_SERVER_TLS to ssl, starttls or none.",
        port,
        settings.email.server_tls,
    )
    return None


def _login_if_configured(smtp_server, email, password):
    """Authenticate only when credentials are set; open relays accept mail without AUTH."""
    if email and password:
        smtp_server.login(email, password)


def _envelope_sender(from_email):
    """Return the bare address for MAIL FROM, even if a display name was configured."""
    return parseaddr(from_email)[1] or from_email


def _ensure_standard_headers(message):
    """Add Date and Message-ID when missing; relays don't reliably add them and receivers may reject mail without them.

    The Message-ID domain comes from the From address, which avoids make_msgid's hostname lookup.
    """
    if "Date" not in message:
        message["Date"] = formatdate(localtime=True)
    if "Message-ID" not in message:
        domain = parseaddr(str(message.get("From") or ""))[1].rpartition("@")[2]
        message["Message-ID"] = make_msgid(domain=domain or "localhost")


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
    _ensure_standard_headers(message)
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
            LOG.error("Server/email/password should not be None")
            return False
    if not isinstance(server, str):
        LOG.error("server %s is not a string", server)
        return False
    if not is_valid_port(port):
        LOG.error("port %s is not valid", port)
        return False
    return True


def _send_email_to_user_smtp(server, port, email, password, message, from_email, rcpt):
    context = ssl.create_default_context()
    context.minimum_version = ssl.TLSVersion.TLSv1_2
    try:
        smtp_server = _create_smtp_connection(server, port, context)
        if smtp_server is None:
            raise RuntimeError(f"Cannot determine SMTP connection type for port {port}")
        with smtp_server:
            _login_if_configured(smtp_server, email, password)
            smtp_server.sendmail(_envelope_sender(from_email), rcpt, message.as_string())

        LOG.info(f"Email sent successfully. from: {from_email}")

    except Exception as e:
        LOG.exception(f"Could not send mail using server {server} and port {port} with email {email}. Error {str(e)}")
        raise


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
        raise


def _is_transient(error):
    """Whether the server refused with a temporary (4xx) error and kept the connection open."""
    if isinstance(error, smtplib.SMTPRecipientsRefused):
        codes = [code for code, _ in error.recipients.values()]
    elif isinstance(error, smtplib.SMTPResponseException):
        codes = [error.smtp_code]
    else:
        return False
    # 421 means the server closed the connection, so there is nothing to send again on.
    return bool(codes) and all(400 <= code < 500 and code != 421 for code in codes)


def _sendmail_with_backoff(smtp_server, from_email, message, delays):
    """Send one message, sending it again after each of `delays` while the server
    answers with a temporary error. Raises the last attempt's error."""
    to = message.get("To")
    envelope_from, body = _envelope_sender(from_email), message.as_string()
    for delay in delays:
        try:
            smtp_server.sendmail(envelope_from, to, body)
            return
        except (smtplib.SMTPResponseException, smtplib.SMTPRecipientsRefused) as e:
            if not _is_transient(e):
                raise
            LOG.warning("Temporary SMTP error sending to %s, retrying in %ss: %s", to, delay, e)
            # smtplib leaves the transaction open when the DATA command itself is refused.
            smtp_server.rset()
            time.sleep(delay)
    smtp_server.sendmail(envelope_from, to, body)


def _send_batch_message(smtp_server, connect, from_email, message, allow_reconnect, delays):
    """Send one message of a batch over `smtp_server`, backing off on temporary
    errors (see _sendmail_with_backoff).

    On SMTPServerDisconnected, reconnects once when `allow_reconnect` and
    retries. Returns (smtp_server, reconnected, error, connection_dead), error
    being the exception the send raised — connection_dead means the connection
    is unrecoverable, so the caller should stop sending on it instead of
    blocking on every remaining message.
    """
    to = message.get("To")
    try:
        _sendmail_with_backoff(smtp_server, from_email, message, delays)
        return smtp_server, False, None, False
    except smtplib.SMTPServerDisconnected as e:
        if not allow_reconnect:
            LOG.warning("Failed to send email to %s: %s", to, e)
            return smtp_server, False, e, True
        LOG.warning("SMTP server disconnected mid-batch, reconnecting once: %s", e)
        try:
            smtp_server = connect()
            _sendmail_with_backoff(smtp_server, from_email, message, delays)
            return smtp_server, True, None, False
        except Exception as e2:
            LOG.warning("Failed to send email to %s after reconnect: %s", to, e2)
            return smtp_server, True, e2, True
    except Exception as e:
        LOG.warning("Failed to send email to %s: %s", to, e)
        return smtp_server, False, e, False


def send_email_batch(messages):
    """Send multiple emails over a single SMTP connection.

    Reconnects once if the server drops the connection mid-batch. Raises when
    any recipient failed to send, so callers can't mistake a partial failure
    for a full success.
    """
    if not messages:
        return

    for message in messages:
        _ensure_standard_headers(message)

    smtp_params = get_smtp_params()
    if smtp_params is not None and _is_valid_smtp_params(smtp_params):
        port, server, email, password, from_email = smtp_params
        ctx = ssl.create_default_context()
        ctx.minimum_version = ssl.TLSVersion.TLSv1_2

        def _connect():
            smtp_server = _create_smtp_connection(server, port, ctx)
            if smtp_server is None:
                raise RuntimeError(f"Cannot determine SMTP connection type for port {port}")
            _login_if_configured(smtp_server, email, password)
            return smtp_server

        try:
            smtp_server = _connect()
        except Exception as e:
            LOG.exception("Could not send batch via SMTP %s:%s - %s", server, port, e)
            raise

        failures = {}
        reconnected = False
        connection_dead = False
        delays = TRANSIENT_RETRY_DELAYS_SECONDS
        for message in messages:
            to = message.get("To")
            if connection_dead:
                failures[to] = smtplib.SMTPServerDisconnected("SMTP connection lost; rest of batch skipped")
                continue
            smtp_server, used_reconnect, error, connection_dead = _send_batch_message(
                smtp_server, _connect, from_email, message, not reconnected, delays
            )
            reconnected = reconnected or used_reconnect
            if error:
                failures[to] = error
                # Waiting didn't clear it, so don't make every remaining message wait too.
                delays = () if _is_transient(error) else delays

        try:
            smtp_server.quit()
        except Exception:
            pass

        if failures:
            raise EmailBatchError(
                f"Failed to send {len(failures)}/{len(messages)} emails in batch: "
                + str({to: str(error) for to, error in failures.items()}),
                failures,
            )
        LOG.info("Batch of %d emails sent successfully via SMTP", len(messages))
        return

    # Fallback to default sendmail for each message. Each send stands alone, as
    # over SMTP, so the error names only the recipients that failed.
    failures = {}
    for message in messages:
        try:
            _send_email_from_default_service(message)
        except Exception as error:
            failures[message.get("To")] = error
    if failures:
        raise EmailBatchError(
            f"Failed to send {len(failures)}/{len(messages)} emails via sendmail: "
            + str({to: str(error) for to, error in failures.items()}),
            failures,
        )


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
