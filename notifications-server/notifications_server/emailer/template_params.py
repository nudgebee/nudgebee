from notifications_server.configs.settings import settings
from notifications_server.utils.datetime_utils import utc_now


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


def _is_custom_branding() -> bool:
    """Check if a custom branding name has been configured."""
    return settings.urls.branding_name != "Nudgebee"


def get_default_template():
    """Return default template parameters for all emails."""
    return {
        "images": {
            "logo_url": settings.urls.branding_logo_url,
            "onboarding_image_url": "" if _is_custom_branding() else settings.urls.branding_onboarding_image_url,
        },
        "texts": {
            "product": settings.urls.branding_name,
            "dont_reply": "Please do not reply to this email",
            "copyright": "Copyright © 2022-%s" % utc_now().year,
            "copyright_year": utc_now().year,
            "address": "Pune",
        },
        "links": {
            "branding_url": settings.urls.base_url,
            "base_url": settings.urls.base_url,
        },
        "branding": {
            "name": settings.urls.branding_name,
            "support_email": settings.urls.branding_support_email,
            "footer_bg_color": settings.urls.branding_footer_bg_color,
            "footer_link_color": settings.urls.branding_footer_link_color,
            "primary_color": settings.urls.branding_primary_color,
            "header_bg_color": settings.urls.branding_header_bg_color,
        },
    }
