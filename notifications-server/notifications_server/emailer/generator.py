"""
Email message generator with Jinja2 template rendering.

Constructs MIME multipart email messages using HTML templates.
"""

import collections.abc
import os
import re
from datetime import datetime, timezone

from jinja2 import Environment, FileSystemLoader

from notifications_server.configs.settings import settings
from notifications_server.emailer.template_params import get_default_template
import logging
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText
from dateutil import parser

LOG = logging.getLogger(__name__)

DATE_FORMAT_EMAIL = "%Y-%m-%d %H:%M"
DEFAULT_FROM_EMAIL = settings.email.from_address
READABLE_DATE_FORMAT = "%Y-%m-%d %H:%M:%S"
LAST_SEEN_DATE_FORMAT = "%b %d, %Y %H:%M"

_CAMEL_CASE_SPLIT = re.compile(r"(?<=[a-z])(?=[A-Z])")


def safe_get(d, path, default=None):
    """Safely get nested dictionary value using a list of keys."""
    for key in path:
        if isinstance(d, dict):
            d = d.get(key, default)
        else:
            return default
    return d


def parse_to_utc(date_value):
    if not date_value:
        return None
    try:
        if isinstance(date_value, datetime):
            dt = date_value
        else:
            dt = parser.parse(date_value)
    except (ValueError, TypeError):
        # Try with microseconds
        try:
            dt = datetime.strptime(date_value, "%Y-%m-%dT%H:%M:%S.%fZ").replace(tzinfo=timezone.utc)
        except (ValueError, TypeError):
            try:
                dt = datetime.strptime(date_value, "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc)
            except (ValueError, TypeError) as e:
                LOG.warning("Unable to parse date '%s': %s", date_value, e)
                return None

    return dt.astimezone(timezone.utc) if dt.tzinfo else dt.replace(tzinfo=timezone.utc)


def format_date_field(date_value, fmt):
    """Parse and format a date string into UTC with a given format."""
    dt = parse_to_utc(date_value)
    return dt.strftime(fmt) if dt else None


def format_agent_date(agent_details):
    if agent_details and "last_connected_at" in agent_details[0]:
        formatted = format_date_field(agent_details[0]["last_connected_at"], DATE_FORMAT_EMAIL)
        if formatted:
            agent_details[0]["last_connected_at"] = formatted


def _set_last_seen(event, field):
    last_seen = format_date_field(event.get(field), LAST_SEEN_DATE_FORMAT)
    if last_seen:
        event["last_seen_formatted"] = f"{last_seen} UTC"


def format_event_date(event_groupings):
    for event in event_groupings:
        for field in ("max_created_at", "created_at"):
            if field == "max_created_at":
                _set_last_seen(event, field)

            formatted = format_date_field(event.get(field), READABLE_DATE_FORMAT)
            if not formatted:
                continue

            event[field] = f"{formatted} UTC"

        if event.get("aggregation_key"):
            event["aggregation_key"] = _CAMEL_CASE_SPLIT.sub(" ", event["aggregation_key"])


def format_string_to_date(data, type_):
    try:
        if type_ == "agents":
            format_agent_date(safe_get(data, ["agent_details", "data", "agent"], []))
        elif type_ == "events":
            format_event_date(safe_get(data, ["data", "event_groupings", "rows"], []))
    except Exception as e:
        LOG.warning("Error while formatting date: %s", e)


def update_template(d, u):
    for k, v in u.items():
        d[k] = update_template(d.get(k, {}), v) if isinstance(v, collections.abc.Mapping) else v
    return d


def format_date(params):
    title = params.get("title")
    if title == "Agent Status Report":
        for acc in params.get("accounts", []):
            format_string_to_date(acc, "agents")
    elif title == "Events Summary":
        events_data = params.get("events_summarised", {})
        format_string_to_date(events_data, "events")


def _generate_context(template_params):
    default_template = get_default_template()
    if not template_params:
        return default_template
    format_date(template_params)
    return update_template(default_template, template_params)


def generate_email(
    to,
    subject,
    template_params,
    template_type=None,
    reply_to_email=None,
    frm=DEFAULT_FROM_EMAIL,
    cc=None,
    bcc=None,
):
    """Generate a MIME multipart email message with HTML body.

    `to` accepts a single address (str) or a list. `cc` is written to the Cc
    header. `bcc` is intentionally NOT written to any header — callers must
    pass the bcc list to the sender separately so those addresses receive the
    message without being disclosed to other recipients.
    """
    msg = MIMEMultipart("related")
    msg["Subject"] = subject
    msg["From"] = frm
    msg["To"] = ", ".join(to) if isinstance(to, list) else to
    if cc:
        msg["Cc"] = ", ".join(cc) if isinstance(cc, list) else cc
    if reply_to_email:
        msg["reply-to"] = reply_to_email
    context = _generate_context(template_params)
    msg.attach(_generate_body(context, template_type))
    return msg


def build_envelope_recipients(to, cc=None, bcc=None):
    """Build the full SMTP RCPT TO list from To/Cc/Bcc inputs.

    Each input can be a single string or a list. Empty/None values are skipped.
    """

    def _as_list(v):
        if not v:
            return []
        return v if isinstance(v, list) else [v]

    return _as_list(to) + _as_list(cc) + _as_list(bcc)


def get_templates_path():
    """Return the path to email templates directory."""
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "templates")


def _safe_length(value):
    """Jinja2 length filter that handles None values."""
    if value is None:
        return 0
    return len(value)


# Cache Jinja2 Environment at module level to avoid re-parsing templates on every render
_jinja_env = Environment(loader=FileSystemLoader(get_templates_path()), autoescape=True)
_jinja_env.filters["length"] = _safe_length


def _generate_body(context, template_type="default"):
    template_type = template_type or "default"
    template = _jinja_env.get_template(f"{template_type}.html")
    return MIMEText(template.render(context), "html")


def render_template_html(template_params, template_type=None):
    """Render a template to an HTML string without wrapping in MIMEText."""
    template_type = template_type or "default"
    context = _generate_context(template_params)
    template = _jinja_env.get_template(f"{template_type}.html")
    return template.render(context)
