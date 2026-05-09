import json
import logging
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText

from notifications_server.configs.settings import get_smtp_params, settings
from notifications_server.exceptions.common_exc import WrongArgumentsException
from notifications_server.processors.base import BaseProcessor
from notifications_server.emailer import (
    generate_email,
    generate_event_template_params,
    render_template_html,
    send_email_async,
    send_email_batch_async,
)

LOG = logging.getLogger(__name__)


class EmailProcessor(BaseProcessor):
    @staticmethod
    def validate_payload(payload):
        if "email" not in payload:
            raise WrongArgumentsException("G0019", 'must provide email address in payload for reaction "email"', [])

    @staticmethod
    def create_tasks(event, reactions):
        tasks = []
        for reaction in reactions:
            payload = json.loads(reaction.payload)
            subject, template_params = generate_event_template_params(event)
            task = EmailProcessor.generate_task(subject, payload.get("email"), template_params=template_params)
            tasks.append(task)
        return tasks

    @staticmethod
    async def process_email_task(email_list, email_task):
        template_type = email_task.get("template_type")
        subject = email_task.get("subject")
        template_params = email_task.get("template_params")
        reply_to_email = email_task.get("reply_to_email")

        smtp_params = get_smtp_params()
        from_email = settings.email.from_address
        if smtp_params is not None and smtp_params[4]:
            from_email = smtp_params[4]

        # Render template once for all recipients
        rendered_html = render_template_html(template_params, template_type)

        messages = []
        for email_address in email_list:
            msg = MIMEMultipart("related")
            msg["Subject"] = subject
            msg["From"] = from_email
            msg["To"] = email_address
            if reply_to_email:
                msg["reply-to"] = reply_to_email
            msg.attach(MIMEText(rendered_html, "html"))
            messages.append(msg)

        # Send all messages over a single SMTP connection
        await send_email_batch_async(messages)

    @staticmethod
    def generate_task(subject, email, template_type=None, template_params=None, reply_to_email=None):
        task = {
            "reaction_type": "EMAIL",
            "subject": subject,
            "to": email,
            "template_type": template_type,
            "template_params": template_params,
            "reply_to_email": reply_to_email,
        }
        return task

    @staticmethod
    async def process_task(task):
        LOG.info("sending email to %s", task.get("to"))
        smtp_params = get_smtp_params()
        from_email = settings.email.from_address
        if smtp_params is not None and smtp_params[4]:
            from_email = smtp_params[4]
        email = generate_email(
            task.get("to"),
            task.get("subject"),
            task.get("template_params"),
            task.get("template_type"),
            task.get("reply_to_email"),
            frm=from_email,
        )
        await send_email_async(email)
