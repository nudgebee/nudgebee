"""Per-channel resolution of the retrieval knobs.

A busy alerts channel and a quiet design channel want different windows and
caps, so every value resolves as **per-channel override -> process default**.
Overrides live in ``messaging_channel_watch.settings`` and are read on each
mention, which is what makes them hot: changing a channel's window takes effect
on the next question, with no restart.

Only keys present in ``_FIELDS`` are honoured. An unknown key in the JSON is
ignored rather than trusted, and a value of the wrong type falls back to the
default instead of propagating a bad config into arithmetic.
"""

import logging
from dataclasses import dataclass

from notifications_server.configs.settings import settings

LOG = logging.getLogger(__name__)


@dataclass(frozen=True)
class ChannelRetrievalConfig:
    thread_message_limit: int
    lookback_minutes: int
    max_context_messages: int
    max_context_tokens: int
    recent_limit: int
    search_limit: int
    author_reference_top_k: int
    recency_halflife_minutes: int
    rank_weight_recency: float
    rank_weight_salience: float
    salience_weight_replies: float
    salience_weight_length: float
    salience_weight_decision: float
    length_norm_chars: int


# override key -> (dataclass field, settings attribute, type, minimum)
# The minimum is a floor, not a range check: a zero or negative window would
# silently retrieve nothing, which is far harder to diagnose than a clamped value.
_FIELDS = (
    ("thread_message_limit", "channel_thread_message_limit", int, 1),
    ("lookback_minutes", "channel_lookback_minutes", int, 1),
    ("max_context_messages", "channel_max_context_messages", int, 1),
    ("max_context_tokens", "channel_context_max_tokens", int, 1),
    ("recent_limit", "channel_context_recent_limit", int, 1),
    ("search_limit", "channel_context_search_limit", int, 0),
    ("author_reference_top_k", "channel_author_reference_top_k", int, 1),
    ("recency_halflife_minutes", "channel_recency_halflife_minutes", int, 1),
    ("rank_weight_recency", "channel_rank_weight_recency", float, 0.0),
    ("rank_weight_salience", "channel_rank_weight_salience", float, 0.0),
    ("salience_weight_replies", "channel_salience_weight_replies", float, 0.0),
    ("salience_weight_length", "channel_salience_weight_length", float, 0.0),
    ("salience_weight_decision", "channel_salience_weight_decision", float, 0.0),
    ("length_norm_chars", "channel_length_norm_chars", int, 1),
)


def _coerce(raw, caster, minimum, fallback):
    """A malformed override must never take down a mention. Bad values log and
    yield to the default rather than raising into the retrieval path."""
    if raw is None:
        return fallback
    # bool is an int subclass; accepting it would turn true into 1 silently.
    if isinstance(raw, bool):
        return fallback
    try:
        value = caster(raw)
    except (TypeError, ValueError):
        LOG.warning("Ignoring channel retrieval override %r: not a %s", raw, caster.__name__)
        return fallback
    return value if value >= minimum else minimum


def resolve(overrides=None) -> ChannelRetrievalConfig:
    """Build the effective config for one channel from its stored overrides."""
    overrides = overrides if isinstance(overrides, dict) else {}
    values = {}
    for key, settings_attr, caster, minimum in _FIELDS:
        default = caster(getattr(settings.notifications, settings_attr))
        values[key] = _coerce(overrides.get(key), caster, minimum, default)
    return ChannelRetrievalConfig(**values)
