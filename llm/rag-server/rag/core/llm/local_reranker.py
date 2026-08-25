"""On-device cross-encoder reranker.

Bi-encoder similarity cannot reject: the query and the document are embedded
separately, so cosine lands in a narrow high band (measured 0.839-0.852 on an
irrelevant result set, against 0.824-0.861 on a relevant one). Ranking works;
thresholding does not, because the two bands overlap.

A cross-encoder reads the pair together and scores whether the document answers
the query, which does separate: on the same result sets the irrelevant tops fell
to 0.82 and below while the relevant tops stayed at 0.97+. That is what makes a
fixed cut-off possible, so the threshold lives here rather than in the caller.

Loading mirrors ``rag.core.embeddings.local``: lazy locked singleton, torch
thread limits from the ONDEVICE_* knobs, optional int8 quantization, and the
same memory accounting so both models report their cost the same way.
"""

import gc
import logging
import os
import threading
from typing import List, Tuple

from utils.config import Config

logger = logging.getLogger(__name__)

_model_instance = None
_model_lock = threading.Lock()


def _quantization_engine(machine: str) -> str:
    """Pick the int8 backend for a CPU architecture.

    macOS reports arm64 and Linux reports aarch64 for the same hardware, so
    matching only one of them sends ARM hosts to fbgemm, which is x86-only and
    raises at load time.
    """
    return "qnnpack" if machine.lower() in ("arm64", "aarch64") else "fbgemm"


def _load_model():
    """Load the cross-encoder, quantizing when RERANKER_QUANTIZE is on."""
    import platform

    import torch
    from sentence_transformers import CrossEncoder

    torch.set_num_threads(Config.ondevice_num_threads)
    torch.set_num_interop_threads(Config.ondevice_interop_threads)
    model = CrossEncoder(Config.reranker_model, max_length=Config.reranker_max_length, device="cpu")

    if Config.reranker_quantize:
        # Measured on ARM, int8 is slower and larger than fp32, so quantization is
        # opt-in rather than the default — turn it on only where it has been
        # measured to help.
        torch.backends.quantized.engine = _quantization_engine(platform.machine())
        model.model = torch.quantization.quantize_dynamic(model.model, {torch.nn.Linear}, dtype=torch.qint8)

    model.model.eval()
    gc.collect()
    return model


def get_reranker():
    """Return the process-wide cross-encoder, loading it on first use."""
    global _model_instance
    if _model_instance is not None:
        return _model_instance

    with _model_lock:
        if _model_instance is not None:
            return _model_instance

        import psutil

        mem_before = psutil.Process(os.getpid()).memory_info().rss / 1024 / 1024
        logger.info(f"Loading reranker model: {Config.reranker_model} (quantize={Config.reranker_quantize})")
        _model_instance = _load_model()
        mem_after = psutil.Process(os.getpid()).memory_info().rss / 1024 / 1024
        logger.info(
            f"Reranker model loaded: {Config.reranker_model} "
            f"(memory: {mem_before:.0f}MB -> {mem_after:.0f}MB, model cost: {mem_after - mem_before:.0f}MB)"
        )
        return _model_instance


def _to_unit_scale(scores) -> List[float]:
    """Map raw model output onto 0-1.

    Some cross-encoders (the BAAI/bge-reranker family) apply a sigmoid inside
    the model and already emit 0-1; others (ms-marco) emit raw logits spanning
    roughly -10..+10. Applying a sigmoid to output that already has one squashes
    everything into 0.5-0.73 and destroys the separation the threshold depends
    on, so detect the scale instead of assuming it.
    """
    import math

    values = [float(s) for s in scores]
    if values and all(-0.01 <= v <= 1.01 for v in values):
        return values
    # Branch on sign rather than the textbook 1/(1+exp(-v)): that form overflows
    # for v < -709, and because rerank fails closed an overflow would cost the
    # query every document rather than one score.
    return [1 / (1 + math.exp(-v)) if v >= 0 else math.exp(v) / (1 + math.exp(v)) for v in values]


def rerank(query: str, docs: List, threshold: float | None = None) -> Tuple[List[Tuple], bool]:
    """Score ``docs`` against ``query`` and drop everything below the threshold.

    ``docs`` are ``(document, score)`` pairs as produced by retrieval; the
    incoming similarity score is replaced by the cross-encoder's.

    Returns ``(kept, ok)``. ``ok`` is False when scoring could not run, which
    the caller must treat as "no usable ranking" rather than falling back to the
    unranked list — unranked results are what put six irrelevant documents into
    a prompt for a question that needed none.
    """
    if not docs:
        return [], True

    cutoff = Config.reranker_threshold if threshold is None else threshold
    try:
        model = get_reranker()
        # page_content is typed str, but it arrives from a Qdrant payload that can
        # carry an explicit null; without the fallback one malformed document
        # would fail the whole batch closed.
        pairs = [(query, (d[0].page_content or "")[: Config.reranker_max_doc_chars]) for d in docs]
        scored = _to_unit_scale(model.predict(pairs))
    except Exception as e:
        logger.warning(f"Reranker failed to score documents: {e}")
        return [], False

    # zip() would silently truncate to the shorter side, so a short score list
    # would drop documents with no error and a success return — the same silent
    # degradation this reranker replaces. Treat it as a scoring failure instead.
    if len(scored) != len(docs):
        logger.warning(f"Reranker scored {len(scored)} of {len(docs)} documents; discarding the batch")
        return [], False

    ranked = sorted(zip(docs, scored), key=lambda x: x[1], reverse=True)
    kept = [(d[0], s) for d, s in ranked if s >= cutoff]
    logger.info(
        f"Reranked {len(docs)} documents, kept {len(kept)} at threshold {cutoff} "
        f"(top={ranked[0][1]:.4f}, bottom={ranked[-1][1]:.4f})"
    )
    return kept, True
