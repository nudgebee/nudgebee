"""Deterministic text analysis for channel awareness.

Everything here is regex and keyword lexicons — no model call. Two groups:

* **Mention-time gates** (:func:`detect_overrides`, :func:`is_self_contained`)
  decide how much history a question is allowed to see.
* **Ingest-time tags** (:func:`classify_decision`, :func:`classify_topic`,
  :func:`extract_people`) label a message once, as it is stored, so ranking and
  author lookups do not have to re-derive them per question.

Per-message model classification would be the single largest cost in this
feature; it is deliberately absent.
"""

import re

# Phrases that let the asker steer retrieval. Matched ONLY against the mention
# the user just wrote — never against retrieved history. If history could
# trigger these, anyone could post "forget everything" into a watched channel
# and silently steer what Nubi is allowed to read. That is the security
# boundary, not a convenience.
_FORGET_PATTERN = re.compile(
    r"\b(forget (that|it|everything|all (of )?that)|start over|starting over|fresh start|ignore (the )?context)\b",
    re.IGNORECASE,
)
_THREAD_ONLY_PATTERN = re.compile(
    r"\b(only (this|the current) thread|just this thread|this thread only|stay in (this|the) thread)\b",
    re.IGNORECASE,
)
_IGNORE_LAST_PATTERN = re.compile(
    r"\b(ignore (your |my )?(last|previous|prior) (answer|reply|response)|disregard (your |the )?(last|previous) "
    r"(answer|reply|response))\b",
    re.IGNORECASE,
)

# Words that point at something outside the question itself. Their presence
# means the question cannot be answered without conversation around it.
_DEICTIC_PATTERN = re.compile(
    r"\b(this|that|these|those|it|its|above|below|earlier|previous|previously|he|she|they|him|her|them|his|their|"
    r"we|our|us|there|then|same|instead|again)\b",
    re.IGNORECASE,
)

# Markers of a settled call. Decisions survive the message cap because losing
# "we went with option B" makes every later answer wrong in the same way.
_DECISION_PATTERN = re.compile(
    r"\b(decided|decision|we('| a)?re going with|let'?s go with|going with|final call|finalized|finalised|agreed|"
    r"approved|sign(ed)? off|ship it|we'?ll use|settled on|conclusion)\b",
    re.IGNORECASE,
)

# Slack renders user references as <@U123>; the id is what survives a display
# name change, so "what did John say" resolves through the id, never the name.
_USER_MENTION_PATTERN = re.compile(r"<@([A-Z0-9]+)(?:\|[^>]*)?>", re.IGNORECASE)

# Coarse buckets used to narrow "what did X say about Y". Keyword-based on
# purpose: it ships on today's database and upgrades to embedding centroids
# later without changing the column.
_TOPIC_LEXICON = (
    ("incident", ("incident", "outage", "sev1", "sev2", "postmortem", "rca", "page", "paged", "downtime")),
    ("deploy", ("deploy", "deployment", "release", "rollout", "rollback", "ship", "canary", "promote")),
    ("database", ("database", "db", "postgres", "rds", "query", "migration", "index", "replica", "failover")),
    ("infra", ("cluster", "kubernetes", "k8s", "node", "pod", "terraform", "helm", "autoscal", "capacity")),
    ("cost", ("cost", "spend", "budget", "bill", "billing", "savings", "pricing", "invoice")),
    ("security", ("security", "cve", "vulnerability", "breach", "auth", "permission", "credential", "token")),
    ("alerting", ("alert", "alarm", "threshold", "noise", "silence", "monitor", "dashboard")),
    ("performance", ("latency", "slow", "throughput", "timeout", "performance", "p95", "p99", "cpu", "memory")),
)

# Words too common to signal shared subject matter between a question and the
# conversation around it.
_STOPWORDS = frozenset(
    """a an and are as at be been but by can could did do does for from had has have how i if in into is it its
    just me my no not of on or our so than that the their them then there these they this to too was we were what
    when where which who why will with would you your""".split()
)

_WORD_PATTERN = re.compile(r"[a-z0-9][a-z0-9'_-]*")


def _content_words(text):
    return {word for word in _WORD_PATTERN.findall((text or "").lower()) if word not in _STOPWORDS and len(word) > 2}


def strip_user_mentions(text):
    """Drop <@U123> tokens. The bot's own mention is not part of the question,
    and leaving ids in skews both the deictic gate and topic overlap."""
    return _USER_MENTION_PATTERN.sub(" ", text or "")


def detect_overrides(mention_text):
    """Retrieval directives found in the mention the user just wrote.

    Returns a dict with ``forget``, ``thread_only`` and ``ignore_last``.
    """
    text = mention_text or ""
    return {
        "forget": bool(_FORGET_PATTERN.search(text)),
        "thread_only": bool(_THREAD_ONLY_PATTERN.search(text)),
        "ignore_last": bool(_IGNORE_LAST_PATTERN.search(text)),
    }


def is_self_contained(mention_text, surrounding_texts):
    """True when the question stands on its own and needs no channel history.

    Both conditions must hold: no word pointing outside the question, and no
    meaningful vocabulary shared with the surrounding conversation. Injecting
    unrelated chatter into "what is the default pod CPU limit" is pure cost and
    pure noise, but the gate stays conservative — either signal keeps context.
    """
    cleaned = strip_user_mentions(mention_text)
    if not cleaned.strip():
        return False
    if _DEICTIC_PATTERN.search(cleaned):
        return False
    question_words = _content_words(cleaned)
    if not question_words:
        return False
    for text in surrounding_texts or ():
        if question_words & _content_words(text):
            return False
    return True


def classify_decision(text):
    return bool(_DECISION_PATTERN.search(text or ""))


def classify_topic(text):
    """Best-matching topic bucket, or None. Scored by how many distinct keywords
    hit so a message mentioning one stray word does not outrank its real subject."""
    lowered = (text or "").lower()
    best_topic = None
    best_score = 0
    for topic, keywords in _TOPIC_LEXICON:
        score = sum(1 for keyword in keywords if keyword in lowered)
        if score > best_score:
            best_topic, best_score = topic, score
    return best_topic


def extract_people(text):
    """Platform user ids referenced in the message, in order, de-duplicated."""
    seen = []
    for user_id in _USER_MENTION_PATTERN.findall(text or ""):
        if user_id not in seen:
            seen.append(user_id)
    return seen
