"""Selects the slice of channel history Nubi reads before answering a mention.

The result is delivered in its own request field — never appended to the user's
question. Channel content is third-party text that Nubi must be able to treat as
reference material, and once it is concatenated into the query no downstream
component can tell the two apart. The rule about how to treat it lives in the
agent's system prompt, where it is operator-controlled, rather than inside the
untrusted content itself.

The governing rule is **scope first, relevance second**: decide where Nubi may
look, then rank inside that scope. The scopes, in the order the flow tries them:

* **Overrides** — the asker said how to treat history ("forget that", "only this
  thread"). Honoured before anything else, and only from the mention text.
* **Self-contained** — a general question that refers to nothing in the room and
  shares no vocabulary with it retrieves nothing.
* **Thread** — a mention inside a thread is answered from that thread. A
  follow-up additionally re-reads whatever the previous answer rested on.
* **Channel** — a mention with no thread falls back to a time window, ranked and
  capped rather than dumped.

On top of the chosen scope, a mention naming people pulls what those people said.
"""

import logging
from datetime import timedelta

from notifications_server.configs.settings import settings
from notifications_server.models.db_base import BaseDB
from notifications_server.repositories import channel_message_repository, channel_watch_repository
from notifications_server.services import channel_analysis, channel_config, channel_ranking
from notifications_server.services.cache import Cache
from notifications_server.utils.datetime_utils import utc_now

LOG = logging.getLogger(__name__)

cache = Cache()

PLATFORM_SLACK = "slack"

# Rough characters-per-token; the cap only needs to be the right order of
# magnitude to keep the block from crowding out the rest of the prompt.
_CHARS_PER_TOKEN = 4

# Citation previews are snapshots that can outlive the retention sweep, so they
# stay short: enough to recognise the message, not enough to preserve it.
_PREVIEW_CHARS = 120


class ChannelContextService:
    def __init__(self, engine, common_service=None):
        self.engine = engine
        self.common_service = common_service
        self._scoped_session = BaseDB.session(self.engine)
        self.session = self._scoped_session()

    def close(self):
        try:
            self._scoped_session.remove()
        except Exception:
            pass

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()
        return False

    def _display_name(self, team_id, author_id):
        """Cached name lookup — a transcript has few distinct authors, but the
        resolution must not cost a Slack call per message."""
        if not author_id:
            return "unknown"
        cached = cache.get_cached_user_name(team_id, author_id)
        if cached:
            return cached
        name = None
        if self.common_service:
            try:
                name = self.common_service.get_slack_user_display_name(team_id, author_id)
            except Exception:
                LOG.debug("Could not resolve display name for %s", author_id, exc_info=True)
        if name:
            cache.cache_user_name(team_id, author_id, name)
            return name
        # Cache the fallback too. Deleted users, bots and transient API errors
        # never resolve, and without this every message from one of them costs
        # another Slack call — a long transcript would hammer the API. Shorter
        # TTL than a real name so a transient failure doesn't pin the raw id.
        cache.cache_user_name(team_id, author_id, author_id, ttl_seconds=3600)
        return author_id

    def _team_domain(self, team_id):
        """Cached workspace-domain lookup for permalinks. A cached empty string
        is a known miss, so an unresolvable workspace costs one API call per
        cache window rather than one per mention."""
        cached = cache.get_cached_team_domain(team_id)
        if cached is not None:
            return cached or None
        domain = None
        if self.common_service:
            try:
                domain = self.common_service.get_slack_team_domain(team_id)
            except Exception:
                LOG.debug("Could not resolve team domain for %s", team_id, exc_info=True)
        cache.cache_team_domain(team_id, domain or "")
        return domain

    @staticmethod
    def _permalink(domain, channel_id, message_id, thread_id):
        if not (domain and message_id):
            return None
        link = f"https://{domain}.slack.com/archives/{channel_id}/p{message_id.replace('.', '')}"
        if thread_id and thread_id != message_id:
            link = f"{link}?thread_ts={thread_id}&cid={channel_id}"
        return link

    def _refs_payload(self, entries, tenant_id, team_id, channel_id):
        """Citation provenance for the block: which channel and which retained
        messages it was assembled from. Previews are short snapshots so the
        citation can outlive the retention sweep without carrying whole
        messages past it."""
        domain = self._team_domain(team_id)
        messages = []
        for entry in entries:
            message_id = entry.get("provider_message_id")
            if not message_id:
                continue
            posted = entry.get("posted_at")
            messages.append(
                {
                    "id": message_id,
                    "author": entry.get("author_name") or self._display_name(team_id, entry.get("author_id")),
                    "posted_at": posted.strftime("%Y-%m-%dT%H:%M:%SZ") if posted else None,
                    "preview": (entry.get("message") or "")[:_PREVIEW_CHARS],
                    "permalink": self._permalink(domain, channel_id, message_id, entry.get("thread_id")),
                }
            )
        if not messages:
            return None
        return {
            "platform": PLATFORM_SLACK,
            "team_id": team_id,
            "channel_id": channel_id,
            "channel_name": channel_watch_repository.get_channel_name(
                self.session, tenant_id=tenant_id, platform=PLATFORM_SLACK, team_id=team_id, channel_id=channel_id
            ),
            "messages": messages,
        }

    def _format(self, entries, team_id):
        """Pair each message with its rendered line, so the budget can drop
        messages and report exactly which ones survived."""
        rendered = []
        for entry in entries:
            posted = entry.get("posted_at")
            stamp = posted.strftime("%b %d %H:%M") if posted else "unknown time"
            author = entry.get("author_name") or self._display_name(team_id, entry.get("author_id"))
            rendered.append((entry, f"[{stamp}] {author}: {entry.get('message', '')}"))
        return rendered

    @staticmethod
    def _apply_budget(rendered, budget_chars):
        """Keep the most recent lines that fit, returning them with the budget
        left over. Trimming from the front drops the oldest context first, which
        is the least useful. The caller threads the remainder between sections so
        the whole block honours one ceiling rather than one per section."""
        kept = []
        used = 0
        for pair in reversed(rendered):
            length = len(pair[1]) + 1
            if used + length > budget_chars:
                break
            used += length
            kept.append(pair)
        return list(reversed(kept)), budget_chars - used

    def _config(self, tenant_id, team_id, channel_id):
        """Per-channel overrides, read per mention so a change takes effect on
        the next question without a restart."""
        overrides = None
        try:
            overrides = channel_watch_repository.get_channel_settings(
                self.session, tenant_id=tenant_id, platform=PLATFORM_SLACK, team_id=team_id, channel_id=channel_id
            )
        except Exception:
            LOG.debug("Could not read channel overrides for %s/%s", team_id, channel_id, exc_info=True)
        return channel_config.resolve(overrides)

    def _thread_scope(self, scope, carry_message_ids, skip_carry):
        """Whatever the previous answer in this thread rested on, and nothing else.

        The thread itself is deliberately not read here. A threaded mention
        already arrives with the thread's own transcript fetched from the
        provider and passed as the question, so reading it again from the
        retained copy would send the same conversation to the model twice —
        once as the question and once as quoted transcript.

        What the provider transcript cannot supply is the material from outside
        the thread that the previous answer used. Re-reading that keeps a
        follow-up resting on the same evidence as the answer it follows up on.
        """
        if skip_carry or not carry_message_ids:
            return []
        # Re-read rather than trust the caller's copy: the ids came from Nubi's
        # own prior turn, but authorisation is re-checked, and anything deleted
        # or expired since simply does not come back.
        return channel_message_repository.list_by_ids(self.session, message_ids=carry_message_ids, **scope)

    def _channel_scope(self, scope, config, exclude_thread_id):
        """No thread to anchor on, so bound by time and rank inside the window."""
        since = utc_now() - timedelta(minutes=config.lookback_minutes)
        return channel_message_repository.list_recent_messages(
            self.session, limit=config.recent_limit, exclude_thread_id=exclude_thread_id, since=since, **scope
        )

    def _rank(self, scope, entries, config):
        """Order by recency and salience, then cap. Reply counts come from one
        grouped query over the candidates' threads rather than a stored counter."""
        if not entries:
            return []
        counts = channel_message_repository.count_replies(
            self.session, thread_ids=[entry.get("thread_id") for entry in entries], **scope
        )
        for entry in entries:
            entry["reply_count"] = counts.get(entry.get("thread_id"), 0)
        channel_ranking.score_messages(entries, utc_now(), config)
        return channel_ranking.apply_cap(entries, config.max_context_messages)

    def build(
        self,
        tenant_id,
        team_id,
        channel_id,
        query_text=None,
        exclude_thread_id=None,
        thread_id=None,
        carry_message_ids=None,
        referenced_user_ids=None,
    ):
        """Returns ``(block, message_ids, refs)`` for a watched channel.

        ``block`` is the transcript to hand to the agent, or None when nothing
        should be read. ``message_ids`` is what was actually read; the caller
        keeps it with the conversation so a follow-up in the same thread can
        rest on the same evidence. ``refs`` is the same read as citation
        provenance — channel plus per-message author/preview/permalink — for
        the UI to show what the answer drew on.
        """
        if not settings.notifications.channel_awareness_enabled:
            return None, [], None
        if not (tenant_id and team_id and channel_id):
            return None, [], None

        config = self._config(tenant_id, team_id, channel_id)
        scope = dict(tenant_id=tenant_id, platform=PLATFORM_SLACK, team_id=team_id, channel_id=channel_id)
        overrides = channel_analysis.detect_overrides(query_text)

        # "Forget that" means exactly that: answer from the question alone.
        if overrides["forget"]:
            return None, [], None
        # "Only this thread" pins scope; with no thread to pin to, nothing.
        thread_only = overrides["thread_only"]
        if thread_only and not thread_id:
            return None, [], None

        if thread_id:
            # "Only this thread" and "ignore your last answer" both mean the
            # carry is not read — not read and then dropped, which would still
            # cost the query and still widen the scope the asker just narrowed.
            primary = []
            supporting = self._thread_scope(scope, carry_message_ids, overrides["ignore_last"] or thread_only)
        else:
            primary = self._rank(scope, self._channel_scope(scope, config, exclude_thread_id), config)
            supporting = []

        # A question that stands on its own gets no history: injecting unrelated
        # chatter is pure cost and pure noise. Compared against the surrounding
        # conversation, so "what is the default pod CPU limit" asked in the
        # middle of a pod-limit debate still keeps its context.
        #
        # Only channel scope is gated. Someone who replied inside a thread is
        # part of that conversation, and the vocabulary test is too weak to
        # overrule that: a thread about "memory pressure on the API server" and
        # a question about "the default cpu limit" share no words, yet the
        # thread is plainly what the question is about. In a channel the room is
        # a far weaker signal of intent, which is where the gate earns its keep.
        if not thread_id and channel_analysis.is_self_contained(
            query_text, [entry.get("message") for entry in primary]
        ):
            return None, [], None

        earlier = (
            [] if thread_only else self._earlier(scope, config, query_text, referenced_user_ids, exclude_thread_id)
        )
        earlier = self._dedupe(supporting + earlier, primary)

        if not primary and not earlier:
            return None, [], None
        block, used, kept = self._render(primary, earlier, team_id, config)
        if not block:
            return None, [], None
        return block, used, self._refs_payload(kept, tenant_id, team_id, channel_id)

    def _earlier(self, scope, config, question, referenced_user_ids, exclude_thread_id):
        """Material from outside the current scope that the question points at:
        what named people said, plus literal keyword hits further back.

        Both reads exclude the mentioned thread. Its transcript already reaches
        the model as the question, so anything matched inside it would be quoted
        back a second time.
        """
        found = []
        # "What did @john say about this?" — resolved from the platform ids the
        # asker actually typed, never by guessing at display names. Nubi's own id
        # is in that list too and is left in deliberately: bot messages are never
        # retained, so it can only ever match zero rows.
        if referenced_user_ids:
            found += channel_message_repository.list_by_author(
                self.session,
                author_ids=list(referenced_user_ids),
                topic=channel_analysis.classify_topic(question),
                limit=config.author_reference_top_k,
                exclude_thread_id=exclude_thread_id,
                **scope,
            )
        if question and config.search_limit:
            found += channel_message_repository.search_messages(
                self.session,
                query_text=question,
                limit=config.search_limit,
                exclude_thread_id=exclude_thread_id,
                **scope,
            )
        return found

    @staticmethod
    def _dedupe(entries, already_included):
        """One message appears once. Keyed on the provider id, which is stable
        across the several queries that can surface the same row."""
        seen = {entry.get("provider_message_id") for entry in already_included}
        unique = []
        for entry in entries:
            key = entry.get("provider_message_id")
            if key in seen:
                continue
            seen.add(key)
            unique.append(entry)
        return sorted(unique, key=lambda entry: (entry.get("posted_at") is None, entry.get("posted_at")))

    def _render(self, primary, earlier, team_id, config):
        """One ceiling for the whole block. The conversation Nubi was asked in
        claims the budget first — it is what makes Nubi sound like it was
        present — and earlier material gets whatever is left."""
        remaining = config.max_context_tokens * _CHARS_PER_TOKEN
        primary_kept, remaining = self._apply_budget(self._format(primary, team_id), remaining)
        earlier_kept = []
        if earlier and remaining > 0:
            earlier_kept, _ = self._apply_budget(self._format(earlier, team_id), remaining)

        sections = []
        if earlier_kept:
            sections.append("Earlier messages that may be relevant:")
            sections.extend(line for _, line in earlier_kept)
            sections.append("")
        if primary_kept:
            sections.append("Recent conversation in this channel:")
            sections.extend(line for _, line in primary_kept)

        block = "\n".join(sections).strip() or None
        if not block:
            return None, [], None
        # Only what survived the budget counts as read, so a follow-up reusing
        # this set gets what Nubi saw rather than what it asked the database for.
        kept = [entry for entry, _ in earlier_kept + primary_kept]
        used = [entry.get("provider_message_id") for entry in kept if entry.get("provider_message_id")]
        return block, used, kept
