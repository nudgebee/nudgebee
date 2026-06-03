"""
Bot Messages Module

This module provides warm, friendly, and human-like messages for the Slack bot.
Messages are designed to make the bot feel like a helpful colleague rather than
a system interface.
"""

import random
from typing import List, Optional

from notifications_server.configs import settings

# ==============================================================================
# ACCOUNT SELECTION MESSAGES
# ==============================================================================

ACCOUNT_SELECTION_PROMPTS: List[str] = [
    "Which account are we diving into?",
    "Which account should I look at?",
    "Which account would you like me to check out?",
    "Which account should we explore together?",
    "Just tell me which account to focus on.",
    "Which account are we working with today?",
    "Which account should I pull up for you?",
    "Which account are we looking at?",
]

ACCOUNT_SELECTION_PROMPTS_PERSONALIZED: List[str] = [
    "Hey {name}! Which account are we diving into?",
    "Hi {name}! Which account should I look at?",
    "Hello {name}! Which account would you like me to check out?",
    "Hey {name}! Which account should I pull up for you?",
]

ACCOUNT_SELECTED_CONFIRMATIONS: List[str] = [
    "Sure thing! Pulling up *{account_name}* for you.",
    "Got it! Switching to *{account_name}* now.",
    "Alright! Loading up *{account_name}* for you.",
    "On it! Connecting to *{account_name}* now.",
    "Roger that! Looking into *{account_name}* right away.",
    "All set! Working with *{account_name}* now.",
]

ACCOUNT_ALREADY_SELECTED: List[str] = [
    "Hey! You've already picked an account for this chat.",
    "Looks like we're already set with an account for this conversation!",
    "No worries, we've got an account locked in for this thread already.",
    "We're good! An account is already selected for this chat.",
]


# ==============================================================================
# THINKING / PROCESSING INDICATORS
# ==============================================================================

THINKING_INDICATORS: List[str] = [
    "Thinking...",
    "Looking into it...",
    "Evaluating...",
    "Analyzing...",
    "Processing...",
    "Digging in...",
    "Investigating...",
    "Checking things out...",
    "Working on it...",
    "Let me see...",
]


# ==============================================================================
# ERROR MESSAGES
# ==============================================================================

LLM_BUSY_MESSAGES: List[str] = [
    "Hold on, I'm still thinking about your last question...",
    "Just a sec! Still working through your previous request...",
    "Hang tight! I'm still processing your earlier question...",
    "One moment! I haven't finished with your last message yet...",
    "Still on it! Give me a moment to wrap up your previous request...",
]

LLM_OFFLINE_MESSAGES: List[str] = [
    "Nubi's offline right now. Couldn't get a response. Try again in a sec!",
    "Hmm, I couldn't reach Nubi. Mind trying again in a moment?",
    "Looks like Nubi's taking a quick break. Give it another shot in a sec!",
    "Nubi seems to be unavailable right now. Try again shortly!",
    "Couldn't connect to Nubi at the moment. Please try again!",
]

GENERIC_ERROR_MESSAGES: List[str] = [
    "Oops! Something went sideways. Mind trying that again?",
    "Hmm, I hit a snag there. Could you try again?",
    "Something didn't go as planned. Let's give it another shot!",
    "I ran into a hiccup. Could you try that again?",
]

SESSION_EXPIRED_MESSAGES: List[str] = [
    "Oops! I lost your session for a sec - please try again!",
    "Looks like our session timed out. Mind starting fresh?",
    "My memory got a bit fuzzy there. Could you start over?",
]

ACCOUNT_NOT_ACCESSIBLE: List[str] = [
    "Unable to find any account you have access to. Please make sure you have valid access.",
    "Hmm, I can't find any accounts you can access. Double-check your permissions?",
    "Looks like there are no accounts available for you right now. Please verify your access.",
]

SIGNUP_URL = f"{settings.base_url}/signup"

USER_NOT_FOUND: List[str] = [
    "It looks like you don't have a Nudgebee account yet! "
    f"Sign up at {SIGNUP_URL} to get started, and I'll be ready to help once you're all set.",
    "I couldn't find a Nudgebee account linked to your email. "
    f"Head over to {SIGNUP_URL} to create one — it only takes a minute!",
    "Hmm, I don't see you in our system yet. "
    f"You can sign up for a free Nudgebee account at {SIGNUP_URL} and then come back and chat with me!",
]


# ==============================================================================
# EMPTY / INVALID INPUT MESSAGES
# ==============================================================================

EMPTY_MESSAGE_RESPONSES: List[str] = [
    "Hey! I didn't catch that - could you try sending your question again?",
    "Hmm, looks like your message was empty. What can I help you with?",
    "I didn't get anything there. Could you try again?",
    "Looks like something got lost! What would you like to know?",
]


# ==============================================================================
# SUCCESS / ACKNOWLEDGMENT MESSAGES
# ==============================================================================

FOLLOWUP_CONFIRMATIONS: List[str] = [
    "Got it — *{option}*. Working on it...",
    "*{option}* it is. Let me dig in...",
    "Noted: *{option}*. Pulling that together now...",
    "Thanks! Going with *{option}*...",
]

PROCESSING_CONFIRMATIONS: List[str] = [
    "Got it! Let me look into that for you.",
    "Sure thing! Working on it now.",
    "On it! Give me just a moment.",
    "Alright, diving in now!",
]


# ==============================================================================
# WELCOME / BOT JOINED MESSAGES
# ==============================================================================

BOT_JOINED_CHANNEL_MESSAGES: List[str] = [
    "Thanks for inviting me! Excited to help here",
    "Hey everyone! Happy to be here. Let me know if you need anything!",
    "Thanks for having me! Ready to help whenever you need.",
    "Hello! Just joined and ready to assist. Mention me anytime!",
]


# ==============================================================================
# FEEDBACK MESSAGES
# ==============================================================================

FEEDBACK_THANKS_MESSAGES: List[str] = [
    "Thank you for the feedback!",
    "Thanks! Your feedback helps me improve.",
    "Appreciate the feedback!",
    "Thanks for letting me know!",
]


# ==============================================================================
# CONVERSATION MANAGEMENT
# ==============================================================================

EXIT_MESSAGES: List[str] = [
    "Thanks! I'm here whenever you need me.",
    "Catch you later! Just mention me when you need help.",
    "See you around! I'll be here when you need me.",
]


# ==============================================================================
# HELPER FUNCTIONS
# ==============================================================================


def get_random_message(messages: List[str], **kwargs) -> str:
    """
    Get a random message from a list and format it with provided kwargs.

    Args:
        messages: List of message templates
        **kwargs: Format arguments for the message template

    Returns:
        A randomly selected and formatted message
    """
    message = random.choice(messages)
    if kwargs:
        return message.format(**kwargs)
    return message


def get_first_name(full_name: Optional[str]) -> Optional[str]:
    """
    Extract first name from a full name string.

    Args:
        full_name: The user's full name

    Returns:
        The first name or None if not available
    """
    if not full_name:
        return None
    parts = full_name.strip().split()
    return parts[0] if parts else None


def get_account_selection_prompt(user_name: Optional[str] = None) -> str:
    """
    Get a random account selection prompt message, optionally personalized.

    Args:
        user_name: Optional user's name for personalization

    Returns:
        A greeting message, personalized if name is provided
    """
    first_name = get_first_name(user_name)

    if first_name:
        return get_random_message(
            ACCOUNT_SELECTION_PROMPTS_PERSONALIZED,
            name=first_name,
        )
    else:
        return get_random_message(ACCOUNT_SELECTION_PROMPTS)


def get_account_selected_confirmation(account_name: str) -> str:
    """Get a random account selection confirmation message."""
    return get_random_message(ACCOUNT_SELECTED_CONFIRMATIONS, account_name=account_name)


def get_account_already_selected() -> str:
    """Get a random 'account already selected' message."""
    return get_random_message(ACCOUNT_ALREADY_SELECTED)


def get_thinking_indicator() -> str:
    """Get a random thinking/processing indicator."""
    return get_random_message(THINKING_INDICATORS)


def get_llm_busy_message() -> str:
    """Get a random message for when LLM is busy with another request."""
    return get_random_message(LLM_BUSY_MESSAGES)


def get_llm_offline_message() -> str:
    """Get a random message for when LLM is offline/unavailable."""
    return get_random_message(LLM_OFFLINE_MESSAGES)


def get_generic_error_message() -> str:
    """Get a random generic error message."""
    return get_random_message(GENERIC_ERROR_MESSAGES)


def get_session_expired_message() -> str:
    """Get a random session expired message."""
    return get_random_message(SESSION_EXPIRED_MESSAGES)


def get_account_not_accessible_message() -> str:
    """Get a random 'no accessible accounts' message."""
    return get_random_message(ACCOUNT_NOT_ACCESSIBLE)


def get_user_not_found_message() -> str:
    """Get a random 'user not found' message with sign-up instructions."""
    return get_random_message(USER_NOT_FOUND)


def get_empty_message_response() -> str:
    """Get a random response for empty/invalid messages."""
    return get_random_message(EMPTY_MESSAGE_RESPONSES)


def get_followup_confirmation(option: str) -> str:
    """Get a random followup confirmation message."""
    return get_random_message(FOLLOWUP_CONFIRMATIONS, option=option)


def get_followup_selection_confirmation(question: str, option: str) -> str:
    """Agentic confirmation that preserves the follow-up question for context."""
    return f'> _I asked:_ "{question}"\n{get_followup_confirmation(option)}'


def get_account_selected_with_context(prompt: str, account_name: str) -> str:
    """Agentic confirmation that preserves the account-selection prompt for context."""
    return f'> _I asked:_ "{prompt}"\n{get_account_selected_confirmation(account_name)}'


def get_processing_confirmation() -> str:
    """Get a random processing confirmation message."""
    return get_random_message(PROCESSING_CONFIRMATIONS)


def get_bot_joined_message() -> str:
    """Get a random bot joined channel message."""
    return get_random_message(BOT_JOINED_CHANNEL_MESSAGES)


def get_feedback_thanks_message() -> str:
    """Get a random feedback thanks message."""
    return get_random_message(FEEDBACK_THANKS_MESSAGES)


def get_exit_message() -> str:
    """Get a random exit/goodbye message."""
    return get_random_message(EXIT_MESSAGES)


def is_conversation_in_progress_error(error_response: Optional[dict]) -> bool:
    """
    Check if an error response indicates a conversation is already in progress.

    Args:
        error_response: The error response from LLM server

    Returns:
        True if the error is a 'conversation in progress' error
    """
    if not error_response:
        return False

    error_msg = str(error_response).lower()
    return "conversation" in error_msg and ("in progress" in error_msg or "already" in error_msg)


def is_budget_exceeded_error(error_response: Optional[dict]) -> bool:
    """
    Check if an error body indicates the organization's LLM usage budget limit
    was exceeded. The LLM server returns these as HTTP 429, so callers detect
    budget errors by status code first; this body sniff is a fallback for when
    the status code is unavailable (e.g. a non-HTTP transport error).

    Args:
        error_response: The parsed JSON error response from the LLM server

    Returns:
        True if the error body looks like a budget-limit-exceeded error
    """
    if not error_response:
        return False

    error_msg = str(error_response).lower()
    return "budget" in error_msg and "exceeded" in error_msg


def get_budget_exceeded_message() -> str:
    """
    User-facing message for a budget-limit-exceeded (HTTP 429) error.

    Kept in sync with the web app's Nubi snackbar copy (``mapUpstreamError`` in
    ``app/src/lib/errorMessages.ts``) so the same branded text appears whether
    the user is chatting with Nubi in the web app or in Slack/Teams/Google Chat.
    """
    return f"Monthly Budget Limit exceeded for this account. Contact {settings.urls.branding_name} Support team."
