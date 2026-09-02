"""Timing benchmark for the scrubber: Tier-1 regex, Tier-1+NER, rehydrate.

Run explicitly to see numbers (it prints a table and never fails on timing):

    PYTHONPATH=. pytest tests/test_scrub_benchmark.py -s -m benchmark

What it reports, per input size:
  * scrub (tier-1)        — regex only, the always-on path
  * scrub (tier-1 + NER)  — adds Presidio/spaCy person/location detection
  * rehydrate             — token -> original substitution

NER cold-start (one-time spaCy model load) is reported separately from the
warm per-call cost, because the model loads once per process and shouldn't
be charged to every request.
"""

import importlib
import statistics
import time

import pytest

# A representative ~RCA log chunk laced with the PII/secret types we detect.
_SAMPLE = (
    "2026-05-29T02:14:07Z payment-api in prod-us-east-1 failing health checks. "
    "On-call engineer Alice Johnson paged from +1 (415) 555-0199. "
    "Root cause: OPENAI_API_KEY=sk-abcDEF123ghi rotated by ops-bot; canary-rollout "
    "in cluster eks-prod-us-east-1 held the stale value. Retry to "
    "https://billing.acme.io/charge?api_key=sssh-very-secret&user=12 returned 401. "
    "Customer Robert Smith (r.smith@example.org, SSN 222-22-2222) called from 10.0.0.5. "
    "Rolled back kafka-broker-anna; reissued Bearer sk-test-xyz123 to auth-svc. "
    "Card 4111 1111 1111 1111 declined for adam-worker."
)

_SIZES = {
    "small (~0.6 KB)": _SAMPLE,
    "medium (~6 KB)": "\n".join(_SAMPLE for _ in range(10)),
    "large (~60 KB)": "\n".join(_SAMPLE for _ in range(100)),
}

_WARM_ITERS = 50


def _bench(fn, iters):
    # One warm-up call (JIT of regex objects, etc.), then timed runs.
    fn()
    samples = []
    for _ in range(iters):
        t0 = time.perf_counter()
        fn()
        samples.append((time.perf_counter() - t0) * 1000.0)  # ms
    samples.sort()
    return {
        "mean": statistics.mean(samples),
        "p50": samples[len(samples) // 2],
        "p95": samples[min(len(samples) - 1, int(len(samples) * 0.95))],
    }


@pytest.mark.benchmark
def test_scrub_rehydrate_timings(monkeypatch, capsys):
    from server.utils.utils import ScrubConfig

    monkeypatch.setattr(ScrubConfig, "enabled", True, raising=False)
    monkeypatch.setattr(
        ScrubConfig, "reversible_types", frozenset({"PERSON", "LOCATION", "EMAIL", "PHONE"}), raising=False
    )
    import server.ee.scrubbing.scrubber as s

    importlib.reload(s)

    have_ner = importlib.util.find_spec("presidio_analyzer") is not None

    # Measure NER cold-start once (model load), separately from warm calls.
    ner_cold_ms = None
    if have_ner:
        t0 = time.perf_counter()
        s.scrub_text(_SAMPLE, ner=True, reversible=True)  # triggers lazy load
        ner_cold_ms = (time.perf_counter() - t0) * 1000.0

    rows = []
    for label, text in _SIZES.items():
        t1 = _bench(lambda: s.scrub_text(text, ner=False, reversible=True), _WARM_ITERS)
        t1ner = None
        if have_ner:
            t1ner = _bench(lambda: s.scrub_text(text, ner=True, reversible=True), _WARM_ITERS)
        # Rehydrate uses the mapping produced by a reversible scrub.
        res = s.scrub_text(text, ner=have_ner, reversible=True)
        rehy = _bench(lambda: s.rehydrate(res.text, res.mapping), _WARM_ITERS)
        rows.append((label, len(text), t1, t1ner, rehy, len(res.mapping)))

    with capsys.disabled():
        print("\n\n=== Scrub / Rehydrate timing (warm, ms; {} iters) ===".format(_WARM_ITERS))
        if ner_cold_ms is not None:
            print(f"NER cold-start (one-time spaCy model load): {ner_cold_ms:.1f} ms")
        else:
            print("NER: presidio/spacy not installed — Tier-1 + rehydrate only")
        header = (
            f"{'input':<18}{'bytes':>7} | {'scrub t1 (p50/p95)':>22} | "
            f"{'scrub +NER (p50/p95)':>24} | {'rehydrate (p50/p95)':>22} | tokens"
        )
        print(header)
        print("-" * len(header))
        for label, nbytes, t1, t1ner, rehy, ntok in rows:
            ner_cell = f"{t1ner['p50']:.3f}/{t1ner['p95']:.3f}" if t1ner else "n/a"
            print(
                f"{label:<18}{nbytes:>7} | "
                f"{t1['p50']:>10.3f}/{t1['p95']:<10.3f} | "
                f"{ner_cell:>24} | "
                f"{rehy['p50']:>10.3f}/{rehy['p95']:<10.3f} | {ntok}"
            )
        print()

    # Sanity (not a perf gate): the functions actually ran and produced output.
    assert rows
    assert all(r[2]["mean"] >= 0 for r in rows)
