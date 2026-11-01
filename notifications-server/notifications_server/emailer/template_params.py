import re

from notifications_server.configs.settings import settings
from notifications_server.utils.datetime_utils import utc_now

_HEX_COLOR = re.compile(r"#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})")


def generate_event_template_params(event):
    """Generate subject and template parameters for event notifications."""
    branding_name = settings.urls.branding_name
    subject = "%s Notification (%s) - %s" % (branding_name, event.get("level"), event.get("evt_class"))
    template_params = {
        "images": {},
        "texts": {
            "object_name": event.get("object_name"),
            "title": f"{branding_name} notifications service",
            "top_text": "You have received the following notification for the customer ",
            "bottom_text1": "You have received this email because you have event notification turned on. Please use ",
            "bottom_text2": "if you want to alter your notification settings.",
        },
    }
    additional_info = _get_additional_info(event)
    template_params["texts"].update(additional_info)
    return subject, template_params


def _get_additional_info(event):
    customer_name = ""
    return {"customer_name": customer_name}


def _relevant_image_name():
    return {}


def _tint(color):
    """Pale background for a pill whose text is `color`: the colour mixed 83% toward white.

    Only 3- and 6-digit hex colours are mixed. The branding row also accepts hex
    with alpha, rgb() and named colours; those get a neutral grey, which sits
    under any text colour.
    """
    match = _HEX_COLOR.fullmatch(color or "")
    if not match:
        return "#F3F4F6"
    digits = match.group(1)
    if len(digits) == 3:
        digits = "".join(c * 2 for c in digits)
    channels = (int(digits[i : i + 2], 16) for i in (0, 2, 4))
    return "#" + "".join(f"{round(c + (255 - c) * 0.83):02X}" for c in channels)


def get_default_template():
    """Return default template parameters for all emails."""
    current_year = utc_now().year
    start_year = settings.urls.branding_copyright_start_year
    copyright_range = str(start_year) if start_year >= current_year else f"{start_year}-{current_year}"
    return {
        "images": {
            "logo_url": settings.urls.branding_logo_url,
        },
        "texts": {
            "product": settings.urls.branding_name,
            "dont_reply": "Please do not reply to this email",
            "copyright": f"Copyright © {copyright_range}",
            "copyright_year": current_year,
            "address": settings.urls.branding_address,
        },
        "links": {
            "branding_url": settings.urls.base_url,
            "base_url": settings.urls.base_url,
            "calendly_url": settings.urls.calendly_url,
        },
        "branding": {
            "name": settings.urls.branding_name,
            "support_email": settings.urls.branding_support_email,
            "footer_bg_color": settings.urls.branding_footer_bg_color,
            "footer_link_color": settings.urls.branding_footer_link_color,
            "primary_color": settings.urls.branding_primary_color,
            "primary_tint_color": _tint(settings.urls.branding_primary_color),
            "header_bg_color": settings.urls.branding_header_bg_color,
        },
    }
