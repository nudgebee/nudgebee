import asyncio
import logging
import smtplib
import ssl
from concurrent.futures import ThreadPoolExecutor
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText
from email.mime.application import MIMEApplication
from typing import List, Optional

from benchmark_server.utils.utils import Config

logger = logging.getLogger(__name__)

# Thread pool for async email sending
_email_executor = ThreadPoolExecutor(max_workers=5, thread_name_prefix="email_sender")


def _send_email_sync(
    to_email: str,
    subject: str,
    body: str,
    attachments: Optional[List[str]] = None,
    cc_emails: Optional[List[str]] = None,
):
    """
    Synchronous function to send email via SMTP.
    """
    if (
        not Config.email_server_host
        or not Config.email_server_user
        or not Config.email_server_password
    ):
        logger.warning("Email configuration missing. Skipping email sending.")
        return

    msg = MIMEMultipart()
    msg["From"] = Config.email_from
    msg["To"] = to_email
    msg["Subject"] = subject

    if cc_emails:
        msg["Cc"] = ", ".join(cc_emails)

    msg.attach(MIMEText(body, "html"))

    if attachments:
        for file_path in attachments:
            try:
                with open(file_path, "rb") as f:
                    part = MIMEApplication(f.read(), Name=file_path.split("/")[-1])
                part["Content-Disposition"] = (
                    f'attachment; filename="{file_path.split("/")[-1]}"'
                )
                msg.attach(part)
            except Exception as e:
                logger.error(f"Failed to attach file {file_path}: {e}")

    context = ssl.create_default_context()
    try:
        if Config.email_server_port == 465:
            with smtplib.SMTP_SSL(
                Config.email_server_host, Config.email_server_port, context=context
            ) as server:
                server.login(Config.email_server_user, Config.email_server_password)
                server.send_message(msg)
        elif Config.email_server_port == 587:
            with smtplib.SMTP(
                Config.email_server_host, Config.email_server_port
            ) as server:
                server.starttls(context=context)
                server.login(Config.email_server_user, Config.email_server_password)
                server.send_message(msg)
        else:
            logger.error(
                f"Unsupported port: {Config.email_server_port}. Use port 465 for SSL or 587 for STARTTLS."
            )
            return

        all_recipients = [to_email] + (cc_emails or [])
        logger.info(f"Email sent successfully to {', '.join(all_recipients)}")

    except Exception as e:
        logger.exception(f"Failed to send email to {to_email}: {e}")


async def send_email_async(
    to_email: str,
    subject: str,
    body: str,
    attachments: Optional[List[str]] = None,
    cc_emails: Optional[List[str]] = None,
):
    """
    Async wrapper for sending emails.
    """
    loop = asyncio.get_running_loop()
    await loop.run_in_executor(
        _email_executor,
        _send_email_sync,
        to_email,
        subject,
        body,
        attachments,
        cc_emails,
    )
