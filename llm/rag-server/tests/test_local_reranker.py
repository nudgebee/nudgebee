"""Cross-encoder reranker: scale handling, thresholding, and fail-closed behaviour.

The model itself is stubbed. What needs pinning here is the logic around it —
which scale the scores are on, what survives the cut, and what happens when
scoring dies. Loading a real 90MB model would test sentence-transformers, not
this module.
"""

import pytest

from rag.core.llm import local_reranker as lr


class _Doc:
    def __init__(self, text):
        self.page_content = text


def _docs(n):
    """n (document, similarity) pairs shaped like retrieval output."""
    return [(_Doc(f"doc {i}"), 0.84) for i in range(n)]


@pytest.fixture(autouse=True)
def _no_cached_model():
    lr._model_instance = None
    yield
    lr._model_instance = None


def _stub(monkeypatch, scores):
    monkeypatch.setattr(lr, "get_reranker", lambda: type("M", (), {"predict": lambda self, pairs: scores})())


# ─── score scale ────────────────────────────────────────────────────────────


def test_logit_output_is_converted_to_unit_scale():
    # ms-marco emits raw logits; 0 is the midpoint, not the floor.
    out = lr._to_unit_scale([-10.0, 0.0, 9.0])
    assert out[0] == pytest.approx(0.0000454, abs=1e-5)
    assert out[1] == pytest.approx(0.5)
    assert out[2] == pytest.approx(0.99988, abs=1e-4)


def test_extreme_negative_logit_does_not_overflow():
    # 1/(1+exp(-v)) raises OverflowError below -709. rerank fails closed, so an
    # overflow would cost the query every document rather than one score.
    assert lr._to_unit_scale([-1000.0, -710.0])[0] == pytest.approx(0.0)


def test_extreme_positive_logit_saturates_to_one():
    assert lr._to_unit_scale([1000.0])[0] == pytest.approx(1.0)


def test_already_unit_scale_output_is_left_alone():
    # BGE rerankers sigmoid internally. Applying a second sigmoid squashes
    # 0..1 into 0.5..0.73 and destroys the separation the threshold needs.
    assert lr._to_unit_scale([0.0, 0.31, 1.0]) == [0.0, 0.31, 1.0]


def test_boundary_values_stay_on_the_unit_branch():
    # Exactly 0 and 1 are legitimate BGE outputs, not logits.
    assert lr._to_unit_scale([0.0, 1.0]) == [0.0, 1.0]


def test_quantization_engine_covers_both_arm_spellings():
    # macOS says arm64, Linux says aarch64 for the same hardware. Matching only
    # one sends ARM hosts to fbgemm, which is x86-only and raises at load time.
    assert lr._quantization_engine("arm64") == "qnnpack"
    assert lr._quantization_engine("aarch64") == "qnnpack"
    assert lr._quantization_engine("AArch64") == "qnnpack"
    assert lr._quantization_engine("x86_64") == "fbgemm"


# ─── thresholding ───────────────────────────────────────────────────────────


def test_keeps_only_documents_at_or_above_threshold(monkeypatch):
    _stub(monkeypatch, [0.99, 0.96, 0.80, 0.10])
    kept, ok = lr.rerank("q", _docs(4), threshold=0.95)
    assert ok is True
    assert [round(s, 2) for _, s in kept] == [0.99, 0.96]


def test_threshold_is_inclusive(monkeypatch):
    _stub(monkeypatch, [0.95])
    kept, _ = lr.rerank("q", _docs(1), threshold=0.95)
    assert len(kept) == 1


def test_all_irrelevant_yields_empty_not_best_of_a_bad_set(monkeypatch):
    # The whole point. Retrieval always returns its nearest k, so "nothing
    # relevant" has to be expressible as zero documents.
    _stub(monkeypatch, [0.82, 0.81, 0.80])
    kept, ok = lr.rerank("get me the pod count", _docs(3), threshold=0.95)
    assert kept == []
    assert ok is True


def test_results_are_ordered_by_relevance_not_retrieval_order(monkeypatch):
    _stub(monkeypatch, [0.96, 0.99, 0.97])
    kept, _ = lr.rerank("q", _docs(3), threshold=0.95)
    assert [round(s, 2) for _, s in kept] == [0.99, 0.97, 0.96]


def test_scores_replace_the_incoming_similarity(monkeypatch):
    # Callers must see cross-encoder relevance, not the 0.84 cosine it replaced.
    _stub(monkeypatch, [0.99])
    kept, _ = lr.rerank("q", _docs(1), threshold=0.95)
    assert kept[0][1] == pytest.approx(0.99)


def test_threshold_defaults_to_config(monkeypatch):
    monkeypatch.setattr(lr.Config, "reranker_threshold", 0.90)
    _stub(monkeypatch, [0.92])
    kept, _ = lr.rerank("q", _docs(1))
    assert len(kept) == 1


# ─── failure handling ───────────────────────────────────────────────────────


def test_scoring_failure_reports_not_ok_and_returns_nothing(monkeypatch):
    def _boom():
        raise RuntimeError("model unavailable")

    monkeypatch.setattr(lr, "get_reranker", _boom)
    kept, ok = lr.rerank("q", _docs(3))
    # Returning the unranked docs here is what shipped six irrelevant
    # documents into a prompt — the caller cannot tell good from bad without
    # a working score, so it must get nothing and know why.
    assert kept == []
    assert ok is False


def test_document_with_null_content_does_not_fail_the_batch(monkeypatch):
    # page_content is typed str but arrives from a Qdrant payload that can carry
    # an explicit null; one bad document must not cost the whole query.
    _stub(monkeypatch, [0.99, 0.98])
    docs = [(_Doc(None), 0.84), (_Doc("real text"), 0.84)]
    kept, ok = lr.rerank("q", docs, threshold=0.95)
    assert ok is True
    assert len(kept) == 2


def test_short_score_list_is_a_failure_not_silent_truncation(monkeypatch):
    # zip() would pair 2 of 3 documents and return success, dropping one with no
    # error. That is the silent degradation this reranker exists to remove.
    _stub(monkeypatch, [0.99, 0.98])
    kept, ok = lr.rerank("q", _docs(3), threshold=0.95)
    assert kept == []
    assert ok is False


def test_empty_score_list_does_not_raise(monkeypatch):
    _stub(monkeypatch, [])
    kept, ok = lr.rerank("q", _docs(3))
    assert kept == []
    assert ok is False


def test_empty_input_is_not_a_failure():
    kept, ok = lr.rerank("q", [])
    assert kept == []
    assert ok is True
