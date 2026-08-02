"""Tests for the universal data scrubber (server.ee.scrubbing)."""

import importlib
import importlib.util

import pytest


@pytest.fixture
def scrubber(monkeypatch):
    """Fresh scrubber module with scrubbing enabled and the default
    reversible set. Reloaded so ScrubConfig changes take effect."""
    from server.utils.utils import ScrubConfig

    monkeypatch.setattr(ScrubConfig, "enabled", True, raising=False)
    monkeypatch.setattr(
        ScrubConfig, "reversible_types", frozenset({"PERSON", "LOCATION", "EMAIL", "PHONE"}), raising=False
    )
    import server.ee.scrubbing.scrubber as s

    importlib.reload(s)
    return s


# --- secrets: opt-in via scrub_secrets=True; always irreversible -------
# Tier-1 secret patterns default to OFF because llm-server runs the
# egressfilter secret gate in-process upstream. These tests verify the
# patterns still work for callers that opt in.


def test_aws_key_redacted(scrubber):
    r = scrubber.scrub_text("rotate AKIAIOSFODNN7EXAMPLE now", reversible=True, scrub_secrets=True)
    assert "AKIAIOSFODNN7EXAMPLE" not in r.text
    assert "[REDACTED_AWS_KEY]" in r.text
    assert r.mapping == {}  # secret must not be reversible


def test_jwt_and_bearer_and_ssn(scrubber):
    jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.sig_part_here"
    r = scrubber.scrub_text(f"auth {jwt} ssn 123-45-6789", reversible=True, scrub_secrets=True)
    assert jwt not in r.text and "123-45-6789" not in r.text
    assert "[REDACTED_JWT]" in r.text and "[REDACTED_SSN]" in r.text
    assert r.mapping == {}


def test_kv_secret(scrubber):
    r = scrubber.scrub_text("OPENAI_API_KEY=sk-abc123 loaded", scrub_secrets=True)
    assert "sk-abc123" not in r.text
    assert "OPENAI_API_KEY=[REDACTED_SECRET]" in r.text


def test_cc_luhn_gate(scrubber):
    r = scrubber.scrub_text("card 4111 1111 1111 1111 and id 1111111111111111", scrub_secrets=True)
    assert "[REDACTED_CC]" in r.text
    assert "1111111111111111" in r.text  # fails Luhn -> survives


def test_secrets_default_off_pass_through(scrubber):
    # The new contract: with scrub_secrets unset (default False), Tier-1
    # secret detectors do NOT run — egressfilter owns that responsibility
    # upstream. Soft PII is still scrubbed.
    raw = "rotate AKIAIOSFODNN7EXAMPLE for jane@acme.co"
    r = scrubber.scrub_text(raw, reversible=True)
    assert "AKIAIOSFODNN7EXAMPLE" in r.text  # secret NOT scrubbed
    assert "jane@acme.co" not in r.text  # email still tokenised
    assert any(v == "jane@acme.co" for v in r.mapping.values())


def test_ip_preserved(scrubber):
    raw = "node 10.0.0.5 unreachable"
    assert scrubber.scrub_text(raw).text == raw


# --- soft PII regex: reversible round-trip -----------------------------


def test_email_irreversible_default(scrubber):
    r = scrubber.scrub_text("ping jane@acme.co")
    assert r.text == "ping [REDACTED_EMAIL]"
    assert r.mapping == {}


def test_email_reversible_roundtrip(scrubber):
    r = scrubber.scrub_text("ping jane@acme.co", reversible=True)
    assert r.text == "ping [EMAIL_1]"
    assert r.mapping == {"[EMAIL_1]": "jane@acme.co"}
    assert scrubber.rehydrate(r.text, r.mapping) == "ping jane@acme.co"


def test_same_value_same_token(scrubber):
    r = scrubber.scrub_text("a@x.com then a@x.com again", reversible=True)
    assert r.text == "[EMAIL_1] then [EMAIL_1] again"
    assert r.mapping == {"[EMAIL_1]": "a@x.com"}


def test_phone_reversible(scrubber):
    r = scrubber.scrub_text("call 212-555-0147 now", reversible=True)
    assert r.text == "call [PHONE_1] now"
    assert scrubber.rehydrate(r.text, r.mapping) == "call 212-555-0147 now"


# --- rehydrate safety ---------------------------------------------------


def test_rehydrate_longest_first(scrubber):
    # [PERSON_1] must not clobber part of [PERSON_10].
    mapping = {"[PERSON_1]": "alice", "[PERSON_10]": "bob"}
    out = scrubber.rehydrate("[PERSON_10] paged [PERSON_1]", mapping)
    assert out == "bob paged alice"


def test_rehydrate_empty_mapping_noop(scrubber):
    assert scrubber.rehydrate("nothing here", {}) == "nothing here"


# --- Phone regex precision (2026-08-01) --------------------------------
# The previous "any 3-4/3-4/3-4 with any separator" pattern hit numeric
# log/metric data on ops text. The rewrite restricts to shapes carrying a
# phone indicator: '+' country code, US parens, or hyphen/space 3-3-4.


def test_phone_rejects_dot_only_metric(scrubber):
    # The exact false-positive from the 2026-07-31 dev evidence run.
    r = scrubber.scrub_text("latency p99 = 100.500.1000", reversible=True)
    assert r.text == "latency p99 = 100.500.1000"
    assert r.mapping == {}


def test_phone_rejects_version_string(scrubber):
    r = scrubber.scrub_text("running app v1.234.5678", reversible=True)
    assert "PHONE" not in r.text
    assert r.mapping == {}


def test_phone_accepts_us_hyphenated(scrubber):
    # Preserved: standard US phone with hyphens keeps working.
    r = scrubber.scrub_text("call 212-555-0147 now", reversible=True)
    assert r.text == "call [PHONE_1] now"


def test_phone_accepts_us_parens(scrubber):
    r = scrubber.scrub_text("call (212) 555-0147 now", reversible=True)
    assert "[PHONE_1]" in r.text


def test_phone_accepts_e164_hyphenated(scrubber):
    # E.164 with '+' country code + hyphens: unambiguous phone (indicator).
    r = scrubber.scrub_text("call +91-98765-43210 now", reversible=True)
    assert "[PHONE_1]" in r.text


def test_phone_accepts_e164_spaced(scrubber):
    r = scrubber.scrub_text("call +1 555 123 4567 now", reversible=True)
    assert "[PHONE_1]" in r.text


def test_phone_no_partial_leak_on_longer_digit_run(scrubber):
    # The `(?<!\d)(?!\d)` boundary guards must prevent a phone-shape
    # prefix from matching PART of a longer digit run and leaving the
    # trailing digits unredacted — that would be worse than not
    # matching at all (partial PII leak). (Gemini review on #35432.)
    #
    # Two outcomes are acceptable here:
    #   - the whole long sequence gets tokenised as one PHONE (still
    #     fully-redacted, unusual-shape phone), OR
    #   - nothing matches (rejected as non-phone).
    # What's NOT acceptable: a short PHONE token followed by leftover
    # digits from the original — that's a leak.

    def assert_no_leak(text: str) -> None:
        r = scrubber.scrub_text(text, reversible=True)
        for value in r.mapping.values():
            # Whatever PHONE token was emitted, the mapped value should
            # not be a strict prefix of some longer digit run — every
            # digit adjacent to the token must have been consumed.
            leftover_after = text.split(value, 1)
            if len(leftover_after) > 1:
                # If the character right after `value` is a digit, we
                # have a partial-match leak.
                tail = leftover_after[1][:1]
                assert (
                    not tail.isdigit()
                ), f"partial-match leak in {text!r}: matched {value!r} but next char {tail!r} is a digit"

    assert_no_leak("call (212) 555-014789 now")
    assert_no_leak("call +1 555 123 456789 now")

    # Sanity: a legit phone with a preceding unrelated number (space-
    # separated) still matches, because the space acts as a separator.
    r3 = scrubber.scrub_text("call 12345 555-123-4567 now", reversible=True)
    assert "[PHONE_1]" in r3.text


# --- Infra allowlist coverage (2026-08-01) -----------------------------
# The 2026-07-31 curl evidence showed spaCy tagging bare ops-tool names
# and English infra vocab as PERSON/LOCATION on ops text. Extended
# _default_infra_allowlist to cover both classes.
#
# These tests use ner=True and require the model to be installed; skip
# gracefully when NER is unavailable (CI without spaCy models).


def _has_ner(scrubber) -> bool:
    return scrubber._get_ner_engine() is not None


def test_infra_allowlist_preserves_ops_tool_name(scrubber):
    if not _has_ner(scrubber):
        pytest.skip("NER engine unavailable in this env")
    # "grafana" hit PERSON_1 in dev evidence — must now be preserved.
    r = scrubber.scrub_text("restart the prometheus, grafana, and alertmanager pods", ner=True, reversible=True)
    assert "PERSON" not in r.text
    assert "LOCATION" not in r.text
    for tool in ("prometheus", "grafana", "alertmanager"):
        assert tool in r.text, f"{tool} should be preserved"


def test_infra_allowlist_preserves_bare_k8s_vocab(scrubber):
    if not _has_ner(scrubber):
        pytest.skip("NER engine unavailable in this env")
    # "node" hit LOCATION_1 in dev evidence — must now be preserved.
    r = scrubber.scrub_text("check node ip-10-0-1-42.us-west-2.compute.internal", ner=True, reversible=True)
    assert "LOCATION" not in r.text
    assert "node" in r.text


def test_rehydrate_no_double_replace(scrubber):
    # Pathological: the original value itself contains a token-shaped
    # substring. Single-pass re.sub must not re-process the substitution.
    mapping = {"[PERSON_1]": "[PERSON_2] from ops", "[PERSON_2]": "alice"}
    out = scrubber.rehydrate("paged [PERSON_1]", mapping)
    assert out == "paged [PERSON_2] from ops"


def test_cc_not_partial_inside_longer_digit_run(scrubber):
    # A long contiguous digit run with a leading "digit-sep-" context.
    # The CC regex would otherwise match the 16-digit Luhn-valid slice
    # and leave the leading "12-" stranded as "12-[REDACTED_CC]".
    raw = "serial 12-4111111111111111"
    out = scrubber.scrub_text(raw, scrub_secrets=True).text
    assert "[REDACTED_CC]" not in out
    assert out == raw


def test_cc_not_partial_with_trailing_digit(scrubber):
    # Trailing digit case: 16-digit Luhn-valid slice followed by more
    # digits must not be partially redacted.
    raw = "id 41111111111111117890"
    out = scrubber.scrub_text(raw, scrub_secrets=True).text
    assert "[REDACTED_CC]" not in out
    assert out == raw


def test_token_for_unknown_type_no_keyerror(scrubber):
    # Defensive: a soft type without a _FIXED entry must not crash.
    session = scrubber._Session(reversible=False, enabled_soft=frozenset())
    assert session.token_for("x", "MADE_UP") == "[REDACTED_MADE_UP]"


# --- disabled / config --------------------------------------------------


def test_disabled_is_noop(monkeypatch):
    from server.utils.utils import ScrubConfig

    monkeypatch.setattr(ScrubConfig, "enabled", False, raising=False)
    import server.ee.scrubbing.scrubber as s

    importlib.reload(s)
    raw = "email a@b.com key AKIAIOSFODNN7EXAMPLE"
    # ScrubConfig.enabled=False is the master kill switch — scrub_secrets is
    # irrelevant in that mode; the whole pipeline is a no-op.
    assert s.scrub_text(raw, reversible=True, scrub_secrets=True).text == raw


# --- batched scrub_texts: unified per-call mapping ---------------------
# Contract relied on by llm-server's scrubllm.go for safe rehydration
# of multipart GenerateContent calls in one HTTP hop.


def test_batch_unified_mapping_order_preserved(scrubber):
    pieces = [
        "ping jane@acme.co",
        "also notify bob@acme.co",
        "ping jane@acme.co again",  # same value as piece 0 → same token
    ]
    r = scrubber.scrub_texts(pieces, reversible=True)
    # Order preserved.
    assert len(r.texts) == len(pieces)
    # Single unified mapping covers everything; tokens are unique within it.
    assert len(set(r.mapping.keys())) == len(r.mapping)
    # Repeated value collapses to one token across pieces.
    jane_token = next(k for k, v in r.mapping.items() if v == "jane@acme.co")
    assert r.texts[0].endswith(jane_token)
    assert r.texts[2].endswith(jane_token + " again")
    # Distinct values get distinct tokens.
    bob_token = next(k for k, v in r.mapping.items() if v == "bob@acme.co")
    assert bob_token != jane_token
    # Round-trip every piece against the unified mapping.
    for original, scrubbed in zip(pieces, r.texts):
        assert scrubber.rehydrate(scrubbed, r.mapping) == original


def test_batch_secrets_default_off(scrubber):
    # Mirrors the single-text default: batch callers also get secrets pass-
    # through unless they explicitly opt in. egressfilter is the upstream
    # gate for the llm-server path.
    pieces = ["rotate AKIAIOSFODNN7EXAMPLE", "page jane@acme.co"]
    r = scrubber.scrub_texts(pieces, reversible=True)
    assert "AKIAIOSFODNN7EXAMPLE" in r.texts[0]
    assert "jane@acme.co" not in r.texts[1]


def test_batch_disabled_is_noop(monkeypatch):
    from server.utils.utils import ScrubConfig

    monkeypatch.setattr(ScrubConfig, "enabled", False, raising=False)
    import server.ee.scrubbing.scrubber as s

    importlib.reload(s)
    pieces = ["a@b.com", "c@d.com"]
    r = s.scrub_texts(pieces, reversible=True)
    assert r.texts == pieces
    assert r.mapping == {}


def test_reversible_type_excluded_falls_back_to_fixed(monkeypatch):
    from server.utils.utils import ScrubConfig

    monkeypatch.setattr(ScrubConfig, "enabled", True, raising=False)
    # EMAIL not in reversible set -> irreversible even when reversible=True.
    monkeypatch.setattr(ScrubConfig, "reversible_types", frozenset({"PERSON"}), raising=False)
    import server.ee.scrubbing.scrubber as s

    importlib.reload(s)
    r = s.scrub_text("ping jane@acme.co", reversible=True)
    assert r.text == "ping [REDACTED_EMAIL]"
    assert r.mapping == {}


# --- disambiguation guard (deterministic, no presidio needed) ----------


def test_embedded_in_identifier_subtoken(scrubber):
    text = "kafka-broker-anna restarted"
    i = text.index("anna")
    assert scrubber._embedded_in_identifier(text, i, i + 4) is True


def test_embedded_in_identifier_slash_ref(scrubber):
    text = "pod/api-server-x4qrt restarted by John"
    i = text.index("x4qrt")
    assert scrubber._embedded_in_identifier(text, i, i + 5) is True


def test_embedded_in_identifier_standalone_name(scrubber):
    text = "Alice Johnson approved"
    assert scrubber._embedded_in_identifier(text, 0, len("Alice Johnson")) is False


def test_embedded_in_identifier_standalone_single(scrubber):
    text = "Anna called the oncall"
    assert scrubber._embedded_in_identifier(text, 0, 4) is False


def test_is_infra_identifier_whole_compound_token(scrubber):
    # spaCy tags the WHOLE hyphenated token as PERSON; preserve it.
    text = "kafka-broker-anna restarted"
    assert scrubber._is_infra_identifier(text, 0, len("kafka-broker-anna")) is True


def test_is_infra_identifier_hostname(scrubber):
    text = "call my-svc.default.svc.cluster.local"
    assert scrubber._is_infra_identifier(text, 0, len(text)) is True


def test_is_infra_identifier_real_name_is_not(scrubber):
    text = "Alice Johnson approved"
    assert scrubber._is_infra_identifier(text, 0, len("Alice Johnson")) is False


# --- 2026-08-02 allowlist extension ---
# Empirical /scrub run against the k8s_orchestrator_lean system-prompt
# corpus (55KB) produced 16 PERSON/LOCATION false-fires. The extended
# _default_infra_allowlist entries below suppress each observed class.
# Every test asserts the token is preserved AND that the "Alice Johnson"
# real-name invariant still holds (no allowlist entry is broad enough
# to match a real given-name/surname pair).


@pytest.mark.parametrize(
    "token",
    [
        "Git",  # measured PERSON_1
        "git",
        "Helm",  # measured PERSON_14
        "helm",
        "k8s",  # measured PERSON_13
        "K8s",  # measured PERSON_6
        "kubectl",
        "docker",
        "terraform",
        "ansible",
        "GitHub",
        "GitLab",
    ],
)
def test_is_infra_identifier_cicd_and_build_tools(scrubber, token):
    text = f"{token} did something"
    assert scrubber._is_infra_identifier(text, 0, len(token)) is True, f"{token!r} should be allowlisted"


@pytest.mark.parametrize(
    "token",
    [
        "JSON",  # measured PERSON_3
        "DB",  # measured PERSON_9
        "METADATA",  # measured PERSON_2
        "H1",  # measured PERSON_7
        "XML",
        "YAML",
        "SQL",
        "API",
        "JWT",
        "OAuth",
        "VPC",
        "HTTP",
        "TLS",
    ],
)
def test_is_infra_identifier_data_format_and_acronym(scrubber, token):
    text = f"return {token} response"
    i = text.index(token)
    assert scrubber._is_infra_identifier(text, i, i + len(token)) is True, f"{token!r} should be allowlisted"


@pytest.mark.parametrize(
    "token",
    [
        "EXPLAIN",  # measured PERSON_11
        "SELECT",
        "INSERT",
        "ANALYZE",
        "VACUUM",
        "JOIN",
        "WHERE",
        "GROUP",
    ],
)
def test_is_infra_identifier_sql_keyword(scrubber, token):
    text = f"{token} plan"
    assert scrubber._is_infra_identifier(text, 0, len(token)) is True, f"{token!r} should be allowlisted"


@pytest.mark.parametrize(
    "token",
    [
        "CrashLoopBackOff",  # measured LOCATION_1
        "OOMKilled",  # measured LOCATION_2
        "ImagePullBackOff",
        "ErrImagePull",
        "ContainerCreating",
        "PodInitializing",
        "Pending",
        "Failed",
        "Terminating",
        "NodeNotReady",
    ],
)
def test_is_infra_identifier_k8s_pod_state(scrubber, token):
    text = f"pod stuck in {token}"
    i = text.index(token)
    assert scrubber._is_infra_identifier(text, i, i + len(token)) is True, f"{token!r} should be allowlisted"


# --- 2026-08-02 (third pass) allowlist extension ---
# Post-#35440 measurement on session 17276ae7 showed 11 remaining PII
# hits on a "Hello" turn. Live /scrub probes surfaced these classes:
#   1. Observability tools missing from the tool regex (Loki, Cortex,
#      Honeycomb, ...)
#   2. Multi-word cloud service names spaCy treats as one PERSON span
#      (Cloud Logging, Cloud SQL, New Relic, ...)
#   3. Linux distribution codenames (Bullseye, Debian Bookworm)
#   4. K8s state name variant not in the previous list
#      (ContainerStatusUnknown)


@pytest.mark.parametrize(
    "token",
    [
        "Loki",
        "loki",
        "Cortex",
        "Tempo",
        "Mimir",
        "Thanos",
        "Honeycomb",
        "Dynatrace",
        "Chronosphere",
        "Datadog",
        "Splunk",
        "Sentry",
        "PagerDuty",
        "CloudWatch",
        "Stackdriver",
    ],
)
def test_is_infra_identifier_extended_observability_tools(scrubber, token):
    text = f"{token} handles that"
    assert scrubber._is_infra_identifier(text, 0, len(token)) is True, f"{token!r} should be allowlisted"


@pytest.mark.parametrize(
    "token",
    [
        "Cloud Logging",
        "Cloud SQL",
        "Cloud Storage",
        "Cloud Run",
        "Cloud Functions",
        "Cloud Trace",
        "Cloud Monitoring",
        "Cloud Build",
        "Cloud Spanner",
        "Cloud Bigtable",
        "Cloud Dataflow",
        "Cloud Composer",
        "New Relic",
        "Sumo Logic",
        "Elastic Cloud",
        "Grafana Cloud",
        "Kafka Connect",
        "Kafka Streams",
        "Cosmos DB",
        "Service Bus",
        "Event Grid",
        "App Service",
        "Application Insights",
    ],
)
def test_is_infra_identifier_multi_word_cloud_service(scrubber, token):
    text = f"{token} was reachable"
    assert scrubber._is_infra_identifier(text, 0, len(token)) is True, f"{token!r} should be allowlisted"


@pytest.mark.parametrize(
    "token",
    [
        "Bullseye",
        "Bookworm",
        "Trixie",
        "Sid",
        "Buster",
        "Jammy",
        "Focal",
        "Bionic",
        "Noble",
        "Alpine",
        "Debian Bookworm",
        "Debian Bullseye",
        "Ubuntu Jammy",
        "Ubuntu Focal",
    ],
)
def test_is_infra_identifier_linux_release_codename(scrubber, token):
    text = f"{token} base image"
    assert scrubber._is_infra_identifier(text, 0, len(token)) is True, f"{token!r} should be allowlisted"


def test_is_infra_identifier_container_status_unknown(scrubber):
    # 2026-08-02 measured on dev — was tagged PERSON despite being a
    # k8s state literal.
    text = "pod state ContainerStatusUnknown"
    i = text.index("ContainerStatusUnknown")
    assert scrubber._is_infra_identifier(text, i, i + len("ContainerStatusUnknown")) is True


def test_extended_allowlist_still_redacts_real_names(scrubber):
    # Cross-category invariant: none of the new entries should be broad
    # enough to preserve a real given-name/surname pair. Whole-token
    # regexes (^...$) mean "Alice Johnson" span never matches ^git$,
    # ^JSON$, ^EXPLAIN$, ^CrashLoopBackOff$, etc. This test pins that.
    text = "Alice Johnson approved"
    assert scrubber._is_infra_identifier(text, 0, len("Alice Johnson")) is False
    # Single-token real given name — also not preserved (the allowlist
    # covers ops/tool/keyword vocabulary specifically; "Alice" is not on it).
    text2 = "Alice paged the oncall"
    assert scrubber._is_infra_identifier(text2, 0, 5) is False


# --- Tier-2 NER (opt-in; skipped without presidio) ----------------------


@pytest.mark.skipif(
    importlib.util.find_spec("presidio_analyzer") is None,
    reason="presidio/spacy not installed (Tier-2 NER is opt-in)",
)
def test_ner_person_reversible_roundtrip(scrubber):
    r = scrubber.scrub_text("Alice Johnson approved the deploy", ner=True, reversible=True)
    assert "Alice Johnson" not in r.text
    assert any(k.startswith("[PERSON_") for k in r.mapping)
    assert scrubber.rehydrate(r.text, r.mapping) == "Alice Johnson approved the deploy"


@pytest.mark.skipif(
    importlib.util.find_spec("presidio_analyzer") is None,
    reason="presidio/spacy not installed (Tier-2 NER is opt-in)",
)
def test_ner_preserves_infra_name_redacts_real_person(scrubber):
    # The dominant ops over-redaction: a name glued into an infra id must
    # survive, while a real standalone name is still redacted.
    r = scrubber.scrub_text("kafka-broker-anna restarted after Alice Johnson updated config", ner=True, reversible=True)
    assert "kafka-broker-anna" in r.text
    assert "Alice Johnson" not in r.text
    assert any(k.startswith("[PERSON_") for k in r.mapping)


@pytest.mark.skipif(
    importlib.util.find_spec("presidio_analyzer") is None,
    reason="presidio/spacy not installed (Tier-2 NER is opt-in)",
)
def test_ner_preserves_pod_ref(scrubber):
    r = scrubber.scrub_text("pod/api-server-x4qrt restarted by John", ner=True, reversible=True)
    assert "pod/api-server-x4qrt" in r.text
    assert "John" not in r.text
