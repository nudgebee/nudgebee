"""Unit tests for budget-exceeded error handling in ``bot_messages``.

When the LLM server rejects a request because the organization's usage budget
limit is exceeded, it responds with HTTP 429 and a body shaped like::

    {"errors": [{"message": "budget: monthly budget limit exceeded for your organization"}]}

Previously the notifications server treated this like any other LLM failure and
replied with a generic "couldn't reach Nubi" offline message. Budget errors are
now detected (primarily by 429 status, with this body sniff as a fallback) and
the user gets the same branded copy shown in the web app's Nubi snackbar.
"""

from notifications_server.configs import settings
from notifications_server.services.bot_messages import (
    LLM_OFFLINE_MESSAGES,
    get_budget_exceeded_message,
    is_budget_exceeded_error,
    is_conversation_in_progress_error,
)

_MONTHLY_BUDGET_BODY = {"errors": [{"message": "budget: monthly budget limit exceeded for your organization"}]}


def test_detects_monthly_budget_error():
    assert is_budget_exceeded_error(_MONTHLY_BUDGET_BODY) is True


def test_detects_daily_count_budget_error():
    body = {"errors": [{"message": "budget: daily investigation count limit exceeded for your organization"}]}
    assert is_budget_exceeded_error(body) is True


def test_ignores_non_budget_and_empty_errors():
    assert is_budget_exceeded_error(None) is False
    assert is_budget_exceeded_error({}) is False
    assert is_budget_exceeded_error({"errors": [{"message": "conversation already in progress"}]}) is False


def test_budget_and_in_progress_are_mutually_exclusive():
    # A budget error must not be misclassified as a busy/in-progress error.
    assert is_conversation_in_progress_error(_MONTHLY_BUDGET_BODY) is False


def test_message_is_branded_and_not_offline_fallback():
    msg = get_budget_exceeded_message()
    assert msg == f"Monthly Budget Limit exceeded for this account. Contact {settings.urls.branding_name} Support team."
    # Branded copy, never the generic offline fallback.
    assert msg not in LLM_OFFLINE_MESSAGES
    # No internal LLM-server "budget:" tag leaks to the user.
    assert "budget:" not in msg.lower()


def test_message_uses_brand_name():
    assert settings.urls.branding_name in get_budget_exceeded_message()
