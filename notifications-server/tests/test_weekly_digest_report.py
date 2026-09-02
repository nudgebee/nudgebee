"""The weekly digest as an attachable PDF.

The Slack message carries a summary; this document carries the whole review.
These tests pin the data shaping and that a real digest actually renders — a
PDF that silently comes out empty would still "succeed" at the call site.
"""

import asyncio

from notifications_server.services import weekly_digest_report as report

DIGEST = {
    "period_start": "2026-08-03",
    "summary": "Capacity dominated the week.",
    "metrics": {
        "events_analysed": 108,
        "events_complete": 95,
        "failure_classes": 25,
        "recurrences": 11,
        "recurrence_pct": 15,
        "noise_pct": 42,
        "services": 34,
    },
    "class_summaries": [
        {
            "label": "Pod memory exhaustion",
            "aggregation_key": "KubePodCrashLooping",
            "account_name": "prod-aws",
            "env": "prod",
            "priority": "P1",
            "events": 9,
            "carried_over_weeks": 3,
            "problem": "llm-server was OOMKilled nine times.",
            "fix": "Raise the memory limit.",
        },
        {
            "label": "Queue backlog",
            "aggregation_key": "RabbitmqTooManyReadyMessages",
            "account_name": "dev-aws",
            "env": "unknown",
            "priority": "P2",
            "events": 13,
            "carried_over_weeks": 0,
            "problem": "Consumers fell behind.",
        },
    ],
    "briefing": {
        "what_broke_lede": "Capacity and configuration changes drove the week.",
        "patterns": [{"title": "llm-server hotspot", "stance": "harden", "body": "Three findings share a cause."}],
        "plan": [{"priority": "P1", "action": "Raise memory limits", "area": "llm-server", "owner": "dev-team"}],
        "hygiene": {"noise": "46 events", "pipeline": "95 of 108", "confidence": "2 low"},
        "carried_over": [{"aggregation_key": "KubePodCrashLooping", "account_name": "prod-aws", "weeks": 3}],
        "resolved": [{"aggregation_key": "StoppedRecurring", "account_name": "dev-aws", "weeks": 2}],
    },
}


def context(**overrides):
    digest = {**DIGEST, **overrides}
    return report.build_context(
        digest, verdict="Three classes have now run 2+ weeks.", period_label="Aug 3 – Aug 9", accounts=2
    )


def test_context_carries_every_section_of_the_review():
    # The point of the PDF is that nothing is summarised away.
    ctx = context()
    assert len(ctx["findings"]) == 2
    assert ctx["patterns"] and ctx["plan"] and ctx["hygiene"]
    assert ctx["carried_over"] and ctx["resolved"]
    assert ctx["lede"] == "Capacity and configuration changes drove the week."


def test_lede_falls_back_to_the_stored_summary():
    ctx = context(briefing={})
    assert ctx["lede"] == "Capacity dominated the week."


def test_tiles_drop_zero_values_and_cap_at_four():
    labels = [label for label, _ in context()["tiles"]]
    assert labels == ["Analysed", "Alert classes", "Recurring", "Noise"], labels

    quiet = context(metrics={"events_analysed": 4, "events_complete": 4})
    assert [la for la, _ in quiet["tiles"]] == ["Analysed"]


def test_finding_meta_names_the_account_and_hides_unknown_env():
    metas = [f["meta"] for f in context()["findings"]]
    assert "prod-aws · prod" in metas[0]
    # "unknown" is an inference gap, not an environment worth printing.
    assert "unknown" not in metas[1]
    assert "dev-aws" in metas[1]


def test_finding_meta_states_recurrence_age():
    metas = [f["meta"] for f in context()["findings"]]
    assert "3 weeks running" in metas[0]
    assert "new this week" in metas[1]


def test_finding_band_tracks_priority():
    findings = context()["findings"]
    assert findings[0]["band"] == report.BAND_P1
    assert findings[1]["band"] == report.BAND_P2


def test_unlabelled_finding_falls_back_to_the_aggregation_key():
    ctx = context(class_summaries=[{"aggregation_key": "KubePodNotReady", "priority": "P1"}])
    assert ctx["findings"][0]["title"] == "KubePodNotReady"


def test_pdf_renders_from_a_real_shaped_digest():
    pdf = report.build_pdf(context())
    assert pdf, "a populated review must produce a document"
    assert pdf.startswith(b"%PDF"), "output should be a PDF"
    assert pdf.count(b"/Type /Page") >= 1


def test_pdf_renders_when_the_briefing_is_empty():
    # A thin week still deserves a document rather than a crash.
    pdf = report.build_pdf(context(briefing={}, class_summaries=[]))
    assert pdf and pdf.startswith(b"%PDF")


def test_pdf_returns_none_rather_than_raising_on_bad_input():
    # The caller treats None as "skip the attachment"; an exception here would
    # take down a notification that has already been delivered.
    assert report.build_pdf({}) is None


def pdf_text(pdf: bytes) -> str:
    from io import BytesIO

    from pypdf import PdfReader

    return "\n".join(page.extract_text() or "" for page in PdfReader(BytesIO(pdf)).pages)


def test_markup_in_a_finding_does_not_abort_the_document():
    # ReportLab's Paragraph parses a mini-XML dialect. A stray closing tag in
    # an LLM-written finding used to take the whole PDF down, which silently
    # demoted the message to a bare link.
    hostile = "error </font> injected"
    pdf = report.build_pdf(context(class_summaries=[{"label": hostile, "aggregation_key": "K", "priority": "P1"}]))
    assert pdf, "a finding containing markup must still produce a document"


def test_angle_brackets_and_ampersands_survive_into_the_pdf():
    # Escaping has to preserve the text, not just avoid the crash: a digest
    # that silently drops "<nil>" is a report that lies about the week.
    ctx = context(
        class_summaries=[
            {
                "label": "pod <nil> reference",
                "aggregation_key": "K",
                "priority": "P1",
                "problem": "Ingress 5xx & timeouts",
            }
        ]
    )
    text = pdf_text(report.build_pdf(ctx))
    assert "<nil>" in text
    assert "5xx & timeouts" in text


def test_explicit_nulls_render_rather_than_crashing_or_printing_none():
    # LLM-written JSON and the digest row both carry explicit nulls, which
    # `.get(key, default)` passes straight through. html.escape(None) raises,
    # and build_pdf swallows that into "no PDF" — so a single null field would
    # silently demote the message to a link. Ints hit the same escape path.
    digest = {
        "period_start": "2026-08-03",
        "summary": None,
        "metrics": {"events_analysed": None, "events_complete": None, "failure_classes": 25, "noise_pct": None},
        "class_summaries": [{"label": "Thing", "aggregation_key": "K", "priority": "P1", "fix": None}],
        "briefing": {
            "patterns": [{"title": None, "stance": None, "body": None}],
            "plan": [{"priority": None, "action": None, "area": None, "owner": None}],
            "hygiene": {"noise": 46, "pipeline": None},
            "carried_over": [{"aggregation_key": None, "weeks": None}],
            "resolved": [{"aggregation_key": None}],
        },
    }
    ctx = report.build_context(digest, verdict="", period_label="Aug 3 – Aug 9", accounts=1)
    pdf = report.build_pdf(ctx)
    assert pdf, "explicit nulls must not take the document down"

    text = pdf_text(pdf)
    assert "None" not in text, f"a null leaked into the rendered document: {text!r}"
    assert "46" in text, "a non-string hygiene value must still render"


def test_zero_is_rendered_not_blanked():
    # `text or ""` would blank a legitimate 0 or False. Both helpers now key on
    # `is None`, so only an actual null becomes empty.
    assert report._esc(0) == "0"
    assert report._esc(False) == "False"
    assert report._esc(None) == ""
    assert report._para(0, report._styles()["body"]).text == "0"


def test_malformed_list_shapes_do_not_cost_the_attachment():
    # The briefing is LLM-written JSONB, so a list of objects sometimes comes
    # back as a list of bare strings. Every section calls .get on these, and the
    # AttributeError used to take the whole PDF down rather than one row.
    digest = {
        "period_start": "2026-08-03",
        "summary": "x",
        "metrics": {"events_analysed": 5, "events_complete": 5},
        "class_summaries": [{"label": "Real", "priority": "P1"}, "a bare string"],
        "briefing": {
            "patterns": ["a bare string"],
            "plan": "not even a list",
            "carried_over": ["KubePodCrashLooping"],
            "resolved": [None],
            "hygiene": {},
        },
    }
    ctx = report.build_context(digest, verdict="", period_label="Aug 3 – Aug 9", accounts=1)
    assert [f["title"] for f in ctx["findings"]] == ["Real"], "non-object entries should be dropped, not rendered"
    assert ctx["patterns"] == [] and ctx["plan"] == [] and ctx["carried_over"] == [] and ctx["resolved"] == []

    pdf = report.build_pdf(ctx)
    assert pdf, "a malformed briefing list must not cost the whole attachment"


def test_a_malformed_hygiene_object_is_ignored():
    # hygiene is the one nested object rather than a list, so the _dicts filter
    # does not cover it; a string or list here used to raise on .get.
    for bad in ("a string", ["a", "list"], 42):
        digest = {
            "period_start": "2026-08-03",
            "summary": "x",
            "metrics": {"events_analysed": 5, "events_complete": 5},
            "class_summaries": [{"label": "Real", "priority": "P1"}],
            "briefing": {"hygiene": bad},
        }
        ctx = report.build_context(digest, verdict="", period_label="Aug 3 – Aug 9", accounts=1)
        assert ctx["hygiene"] == []
        assert report.build_pdf(ctx), f"hygiene={bad!r} must not cost the attachment"


def test_report_filename_is_dated():
    assert report.report_filename("2026-08-03") == "weekly-digest-2026-08-03.pdf"


def test_fetch_digest_rejects_an_unparseable_period_without_touching_the_db():
    # asyncpg binds a date column as a real date, so the string has to be parsed
    # before binding — a bad value must fail here, not inside the driver.
    class ExplodingSession:
        async def execute(self, *_args, **_kwargs):  # pragma: no cover - must not run
            raise AssertionError("the query should not be attempted")

    result = asyncio.run(report.fetch_digest(ExplodingSession(), "tenant", "not-a-date"))
    assert result is None
