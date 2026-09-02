"""Weekly digest renderers, one per channel.

The producer (llm-server) ships a summary of a much larger document, so these
tests pin the things that make the summary honest: the headline, the account on
every finding, the overflow count, and the deep link back to the full review.
"""

import json

from notifications_server.message_templates import template_mapping
from notifications_server.message_templates.slack.weekly_digest_events import (
    STRIPE_P1,
    STRIPE_P2,
    get_weekly_digest_events_message_params,
    get_weekly_digest_events_message_template,
    digest_headline,
    digest_link,
    digest_verdict,
    scoreboard_pairs,
)

BASE_PAYLOAD = {
    "title": "Weekly Digest",
    "period_start": "2026-08-03",
    "period_end": "2026-08-09",
    "period_label": "Aug 3 – Aug 9",
    "events_analysed": 108,
    "events_complete": 95,
    "completion_pct": 88,
    "failed_events": 6,
    "failure_classes": 25,
    "services": 11,
    "recurrences": 11,
    "recurrence_pct": 15,
    "noise_pct": 42,
    "p1_pct": 77,
    "lede": "Capacity dominated the week.",
    "total_findings": 14,
    "more_findings": 11,
    "accounts_named": 4,
    "base_url": "https://app.example.com",
    "digest_url": "https://app.example.com/home?bcortex=digests",
    "top_findings": [
        {
            "label": "Pod memory exhaustion",
            "aggregation_key": "KubePodCrashLooping",
            "account_name": "prod-aws",
            "cloud_account_id": "acct-a",
            "headline": "llm-server was OOMKilled 9 times.",
            "priority": "P1",
            "events": 9,
            "carried_over_weeks": 3,
            "env": "prod",
        },
        {
            "label": "Queue backlog",
            "aggregation_key": "RabbitmqTooManyReadyMessages",
            "account_name": "dev-aws",
            "cloud_account_id": "acct-b",
            "headline": "Consumers fell behind for six hours.",
            "priority": "P2",
            "events": 13,
            "carried_over_weeks": 0,
        },
    ],
}


def params(**overrides):
    return get_weekly_digest_events_message_params(**{**BASE_PAYLOAD, **overrides})


def test_all_four_channels_are_registered():
    entry = template_mapping["weekly_digest_events"]
    for channel in ("slack", "ms_teams", "google_chat", "discord"):
        assert entry[channel] is not None, f"{channel} renderer missing"


def test_every_channel_renders_without_error():
    entry = template_mapping["weekly_digest_events"]
    p = params()
    for channel in ("slack", "ms_teams", "google_chat", "discord"):
        out = entry[channel](p)
        assert out, f"{channel} produced an empty payload"
        json.dumps(out)  # must be serialisable for the transport


def test_headline_counts_failure_classes():
    assert digest_headline(params()) == "25 alert classes across 4 accounts"


def test_headline_ignores_pipeline_failures():
    # failed_events is pipeline health ("did the analysis finish"), not incident
    # count. Using it here reported "1 failure this week" for a week carrying 27
    # failure classes — a busy week reading as a quiet one.
    busy = params(failed_events=1, failure_classes=27)
    assert "27 alert classes" in digest_headline(busy)
    assert "1 failure this week" not in digest_headline(busy)


def test_headline_falls_back_to_findings_without_a_class_count():
    assert digest_headline(params(failure_classes=0, total_findings=3)) == "3 alert classes across 4 accounts"


def test_headline_says_so_when_nothing_broke():
    assert digest_headline(params(failure_classes=0, total_findings=0)).startswith("No failures this week")


def test_headline_omits_account_clause_for_a_single_account():
    assert "across" not in digest_headline(params(accounts_named=1))


def test_scoreboard_drops_zero_valued_lines():
    # "Noise 0% of events" costs a row to say nothing.
    labels = [label for label, _ in scoreboard_pairs(params(noise_pct=0))]
    assert "Noise" not in labels
    assert "Analysed" in labels, "analysed always shows — 0 of 0 is itself the story"


def test_scoreboard_keeps_populated_lines():
    labels = [label for label, _ in scoreboard_pairs(params())]
    assert labels == ["Analysed", "Alert classes", "Recurring", "Noise"]


def test_scoreboard_stays_even_so_the_grid_never_orphans():
    # Slack lays `fields` out two per row, so an odd count renders as a lone
    # tile hanging under a full row. Dropping a zero figure must not cause that.
    for overrides in (
        {"noise_pct": 0},
        {"recurrences": 0},
        {"noise_pct": 0, "recurrences": 0, "new_incidents": 4, "p1_pct": 60},
        {},
    ):
        pairs = scoreboard_pairs(params(**overrides))
        assert len(pairs) % 2 == 0, f"odd grid for {overrides}: {[p[0] for p in pairs]}"


def test_scoreboard_pads_from_reserves_not_from_zeros():
    # The padding figure must itself be real — padding with another zero would
    # trade one empty tile for another.
    labels = [label for label, _ in scoreboard_pairs(params(noise_pct=0, new_incidents=9))]
    assert "New this week" in labels
    assert "Noise" not in labels


def test_scoreboard_leaves_odd_count_when_nothing_can_pad_it():
    # Better a single orphan than a fabricated figure.
    pairs = scoreboard_pairs(params(failure_classes=0, recurrences=0, noise_pct=0, new_incidents=0, p1_pct=0))
    assert [label for label, _ in pairs] == ["Analysed"]


def test_verdict_is_above_the_figures_and_lede_below():
    # One short bold judgement before any numbers; the model's multi-sentence
    # lede stays below them, where it cannot bury the message.
    out = get_weekly_digest_events_message_template(params())
    idx = {"verdict": None, "fields": None, "lede": None}
    for i, b in enumerate(out["blocks"]):
        if b.get("type") == "section" and "fields" in b:
            idx["fields"] = i
        elif b.get("type") == "section" and digest_verdict(params()) in b.get("text", {}).get("text", ""):
            idx["verdict"] = i
        elif b.get("type") == "context" and BASE_PAYLOAD["lede"] in b["elements"][0]["text"]:
            idx["lede"] = i
    assert idx["verdict"] is not None and idx["fields"] is not None and idx["lede"] is not None, idx
    assert idx["verdict"] < idx["fields"] < idx["lede"], idx


def blocks_of(out, kind):
    return [b for b in out["blocks"] if b.get("type") == kind]


def all_text(out):
    """Every string rendered anywhere in the message."""
    parts = []
    for b in out["blocks"]:
        if "text" in b and isinstance(b["text"], dict):
            parts.append(b["text"]["text"])
        for f in b.get("fields", []):
            parts.append(f["text"])
        for e in b.get("elements", []):
            parts.append(e.get("text", {}).get("text", "") if isinstance(e.get("text"), dict) else e.get("text", ""))
    for a in out.get("attachments", []):
        parts.append(a.get("text", "") or "")
        for b in a.get("blocks", []):
            if isinstance(b.get("text"), dict):
                parts.append(b["text"]["text"])
            for e in b.get("elements", []):
                parts.append(e.get("text", "") if isinstance(e.get("text"), str) else "")
    return "\n".join(parts)


def test_findings_are_banded_attachments_without_blocks():
    # Slack stamps an "Added by <app>" row on any attachment carrying Block Kit
    # `blocks` — one per finding. Every other template in this service uses
    # top-level blocks plus legacy attachments, and none of them are stamped.
    out = get_weekly_digest_events_message_template(params())
    cards = [a for a in out["attachments"] if a.get("text")]
    assert cards, "findings should be coloured attachments"
    assert all(a.get("color") for a in cards[:-1]), "every finding card needs its severity band"
    assert all("blocks" not in a for a in out["attachments"]), "blocks inside an attachment earn the byline"


def test_finding_band_colour_tracks_priority():
    out = get_weekly_digest_events_message_template(params())
    colors = [a["color"] for a in out["attachments"]]
    assert colors[0] == STRIPE_P1
    assert colors[1] == STRIPE_P2


def test_footer_attachment_carries_no_blocks():
    # A `blocks` key here would earn a second "Added by <app>" byline for a row
    # that is only a button.
    out = get_weekly_digest_events_message_template(params())
    footer = out["attachments"][-1]
    assert "blocks" not in footer


def test_scoreboard_rows_are_separate_blocks():
    # Four fields in one section makes Slack run the pairs together
    # ("95 of 108Classes"); a section per pair gives each row its own spacing.
    out = get_weekly_digest_events_message_template(params())
    field_sections = [b for b in out["blocks"] if b.get("type") == "section" and "fields" in b]
    assert len(field_sections) == 2, "four figures should render as two rows"
    assert all(len(b["fields"]) == 2 for b in field_sections)


def card_captions(out):
    """The italic facts line at the foot of each finding card."""
    caps = []
    for a in out.get("attachments", []):
        for line in (a.get("text") or "").split("\n"):
            if line.startswith("_") and line.endswith("_"):
                caps.append(line.strip("_"))
    return caps


def test_each_finding_has_a_caption_in_small_text():
    # A context block is Slack's only genuinely secondary text style; it is what
    # separates a finding's facts from its description.
    out = get_weekly_digest_events_message_template(params())
    caps = card_captions(out)
    assert any("prod-aws" in c and "events" in c for c in caps), caps


def test_caption_does_not_repeat_the_priority():
    # The colour band already carries severity; printing it again in the caption
    # spends the line twice.
    out = get_weekly_digest_events_message_template(params())
    assert not any("P1" in c or "P2" in c for c in card_captions(out)), card_captions(out)


def test_caption_names_the_account_and_hides_unknown_env():
    out = get_weekly_digest_events_message_template(
        params(
            top_findings=[
                {"label": "A", "account_name": "prod-aws", "env": "prod", "priority": "P1", "events": 3},
                {"label": "B", "account_name": "dev-aws", "env": "unknown", "priority": "P1", "events": 2},
            ]
        )
    )
    caps = card_captions(out)
    assert any(c.startswith("prod-aws · prod") for c in caps), caps
    # "unknown" is an inference gap, not an environment worth printing.
    assert not any("unknown" in c for c in caps), caps


def test_verdict_leads_with_staleness_over_volume():
    # A class nobody has fixed in weeks outranks a loud one-off.
    assert digest_verdict(params()) == "One class has now run 2+ weeks."
    fresh = params(
        noise_pct=55,
        top_findings=[{"label": "A", "priority": "P1", "carried_over_weeks": 0, "events": 5}],
    )
    assert digest_verdict(fresh) == "55% of this week's events were noise."


def test_verdict_says_so_when_nothing_broke():
    quiet = params(noise_pct=0, total_findings=0, top_findings=[])
    assert digest_verdict(quiet) == "Nothing broke this week."


def test_recurrence_age_shows_in_the_caption():
    out = get_weekly_digest_events_message_template(params())
    caps = card_captions(out)
    assert any("3 wks running" in c for c in caps), caps
    assert any("new this week" in c for c in caps), caps


def test_slack_footer_points_at_the_attachment_not_a_link():
    # The complete review rides this message as a PDF, so a button offering the
    # same thing behind a login is noise. The other channels keep the link --
    # the attachment is Slack-only.
    out = get_weekly_digest_events_message_template(params())
    footer = out["attachments"][-1]
    assert "actions" not in footer, "Slack should not offer a link when the PDF is attached"
    assert "attached" in footer["text"].lower()
    assert "+11 more" in footer["text"]


def test_overflow_line_absent_when_everything_fits():
    out = get_weekly_digest_events_message_template(params(more_findings=0))
    text = out["attachments"][-1].get("text", "")
    assert "more in the full review" not in text
    assert "attached" in text.lower(), "the attachment pointer always shows"


def test_stays_within_platform_limits():
    out = get_weekly_digest_events_message_template(params())
    assert len(out["blocks"]) <= 50
    assert len(out["attachments"]) <= 20


def test_other_channels_still_carry_the_link():
    # Only Slack gets the PDF; removing digest_link everywhere would leave Teams,
    # Google Chat and Discord with no route to the review at all.
    entry = template_mapping["weekly_digest_events"]
    p = params()
    assert "bcortex=digests" in entry["google_chat"](p)["text"]
    discord = " ".join(e.get("description", "") for e in entry["discord"](p)["embeds"])
    assert "bcortex=digests" in discord
    assert entry["ms_teams"](p)["actions"][0]["url"].endswith("/home?bcortex=digests")


def test_finding_falls_back_to_the_aggregation_key_when_unlabelled():
    out = get_weekly_digest_events_message_template(
        params(top_findings=[{"aggregation_key": "KubePodNotReady", "priority": "P1", "account_name": "prod-aws"}])
    )
    assert "KubePodNotReady" in all_text(out)


def test_aggregation_key_is_not_shown_when_a_label_exists():
    # Dropped deliberately: the most jargon-heavy line, and a whole row per card.
    out = get_weekly_digest_events_message_template(params())
    assert "KubePodCrashLooping" not in all_text(out)


def test_link_rebuilt_from_base_url_lands_on_home():
    # The producer normally supplies digest_url, so this fallback is the one
    # path no other test covers. It has to point at /home: the root route
    # redirects there without carrying its query, dropping the param.
    assert digest_link(params(digest_url="")) == "https://app.example.com/home?bcortex=digests"


def test_a_relative_digest_url_is_ignored_in_favour_of_the_base_url():
    # Chat clients cannot resolve a relative path, and the producer emits one
    # whenever its base URL is unset — trusting it would ship a dead link.
    assert digest_link(params(digest_url="/home?bcortex=digests")) == "https://app.example.com/home?bcortex=digests"


def test_discord_and_gchat_carry_the_link_and_overflow():
    entry = template_mapping["weekly_digest_events"]
    p = params()

    gchat = entry["google_chat"](p)["text"]
    assert "https://app.example.com/home?bcortex=digests" in gchat
    assert "11 more" in gchat
    assert "prod-aws" in gchat

    discord = entry["discord"](p)
    body = " ".join(e.get("description", "") for e in discord["embeds"])
    assert "bcortex=digests" in body
    assert "11 more" in body


def test_teams_action_points_at_the_review():
    out = template_mapping["weekly_digest_events"]["ms_teams"](params())
    assert out["actions"][0]["url"] == "https://app.example.com/home?bcortex=digests"
    assert any("prod-aws" in str(b.get("text", "")) for b in out["body"])


def test_empty_finding_list_still_renders():
    # The producer skips empty weeks, but a template that explodes on an empty
    # list would turn a benign payload into a dead-letter message.
    out = get_weekly_digest_events_message_template(params(top_findings=[], total_findings=0, more_findings=0))
    assert out["blocks"], "message must still render"
    assert out["attachments"][-1]["text"], "the footer must survive"
