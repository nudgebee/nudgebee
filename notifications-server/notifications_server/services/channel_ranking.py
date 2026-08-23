"""Arithmetic ranking for channel-scope candidates.

Once scope is fixed, what survives the cap is decided by a weighted score, not
by recency alone — a flat "last N messages" is exactly the dump this design
replaces. Everything is arithmetic over columns already stored, so ranking is
free, reproducible for a fixed input, and testable without a model.
"""


def _recency(age_minutes, halflife_minutes):
    """Exponential decay, halving every half-life. Bounded 0..1."""
    if age_minutes <= 0:
        return 1.0
    return 0.5 ** (age_minutes / float(halflife_minutes))


def _salience(entry, config):
    length = len(entry.get("message") or "")
    return (
        config.salience_weight_replies * float(entry.get("reply_count") or 0)
        + config.salience_weight_length * min(length / float(config.length_norm_chars), 1.0)
        + config.salience_weight_decision * (1.0 if entry.get("is_decision") else 0.0)
    )


def _normalise(values):
    """Min-max within the candidate set, so salience is comparable to recency's
    0..1 range. A set with no spread contributes nothing rather than dividing by
    zero or letting one arbitrary message dominate."""
    if not values:
        return []
    low, high = min(values), max(values)
    if high - low < 1e-9:
        return [0.0] * len(values)
    return [(value - low) / (high - low) for value in values]


def score_messages(entries, now, config):
    """Attach a ``_score`` to each candidate. Highest is most worth keeping."""
    saliences = _normalise([_salience(entry, config) for entry in entries])
    for entry, salience in zip(entries, saliences):
        posted_at = entry.get("posted_at")
        age_minutes = max((now - posted_at).total_seconds() / 60.0, 0.0) if posted_at else 0.0
        entry["_score"] = (
            config.rank_weight_recency * _recency(age_minutes, config.recency_halflife_minutes)
            + config.rank_weight_salience * salience
        )
    return entries


def apply_cap(entries, max_messages):
    """Keep the highest-scoring messages, then restore chronological order.

    Decisions are exempt from the cap: dropping "we went with option B" makes
    every later answer wrong in the same way, and there are few enough of them
    that keeping them cannot blow up the set. The token budget applied
    downstream is the backstop that bounds the block either way.
    """
    if len(entries) <= max_messages:
        return _chronological(entries)
    decisions = [entry for entry in entries if entry.get("is_decision")]
    rest = sorted((entry for entry in entries if not entry.get("is_decision")), key=_rank_key, reverse=True)
    kept = decisions + rest[: max(max_messages - len(decisions), 0)]
    return _chronological(kept)


def _rank_key(entry):
    return entry.get("_score") or 0.0


def _chronological(entries):
    """A transcript only reads correctly in the order it was said."""
    return sorted(entries, key=lambda entry: (entry.get("posted_at") is None, entry.get("posted_at")))
