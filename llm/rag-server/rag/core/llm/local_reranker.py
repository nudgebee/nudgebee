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
import time
from typing import List, Tuple

from utils.config import Config

logger = logging.getLogger(__name__)

_model_instance = None
_model_lock = threading.Lock()
# Admission control for the forward pass. Sized from config rather than the
# threadpool because the bound that matters is CPU cores, not request slots.
_inference_slots = threading.Semaphore(Config.reranker_max_concurrency)


def _quantization_engine(machine: str) -> str:
    """Pick the int8 backend for a CPU architecture.

    macOS reports arm64 and Linux reports aarch64 for the same hardware, so
    matching only one of them sends ARM hosts to fbgemm, which is x86-only and
    raises at load time.
    """
    return "qnnpack" if machine.lower() in ("arm64", "aarch64") else "fbgemm"


_DTYPE_NAMES = ("float32", "bfloat16")


def _resolve_dtype(name, torch_module):
    """Map a configured dtype name onto a torch dtype.

    An unrecognised name falls back to float32 rather than raising: a typo in a
    values file must not stop the server from starting.
    """
    if name not in _DTYPE_NAMES:
        logger.warning(f"Unknown RAG_RERANKER_DTYPE {name!r}; falling back to float32")
        return torch_module.float32
    return getattr(torch_module, name)


# A bfloat16 matmul that is not hardware-accelerated is emulated, and emulation
# measured 6-10x slower than float32. Anything past this ratio is treated as
# emulated; the gap between real acceleration and emulation is far wider than
# the noise a busy pod adds, so the bound is deliberately loose.
_BF16_SLOWDOWN_LIMIT = 1.5


def _bf16_acceleration(torch_module):
    """Name the x86 bfloat16 path this CPU advertises, or None.

    Used only to make the startup log specific. It cannot decide whether
    bfloat16 is worth using, because these probes do not exist on ARM — that is
    what ``_bf16_is_fast`` is for.
    """
    for probe, name in (("_is_amx_tile_supported", "AMX"), ("_is_avx512_bf16_supported", "AVX512-BF16")):
        check = getattr(getattr(torch_module, "cpu", None), probe, None)
        try:
            if callable(check) and check():
                return name
        except Exception:  # probe is best-effort; never block startup on it
            continue
    return None


def _bf16_is_fast(torch_module):
    """Time a small bfloat16 matmul against float32 on this machine.

    Feature flags are per-architecture and torch only exposes them for x86, so a
    flag check mislabels every ARM host. Timing the operation we actually care
    about needs no per-CPU knowledge and works anywhere, including hardware that
    does not exist yet.

    Returns True when bfloat16 keeps up, False when it is clearly slower, and
    None when the probe could not run at all.
    """
    import time

    try:
        size, reps = 256, 5
        f32 = torch_module.randn(size, size)
        pair = {
            "f32": (f32, torch_module.randn(size, size)),
            "bf16": (f32.to(torch_module.bfloat16), torch_module.randn(size, size).to(torch_module.bfloat16)),
        }

        def elapsed(x, y):
            with torch_module.no_grad():
                x @ y  # warm the kernel so the first-call cost is not measured
                start = time.perf_counter()
                for _ in range(reps):
                    x @ y
                return time.perf_counter() - start

        f32_secs = elapsed(*pair["f32"])
        bf16_secs = elapsed(*pair["bf16"])
        if f32_secs <= 0:
            return None
        return bf16_secs <= f32_secs * _BF16_SLOWDOWN_LIMIT
    except Exception:
        return None


def _load_model():
    """Load the cross-encoder at the configured dtype, quantizing when asked."""
    import platform

    import torch
    from sentence_transformers import CrossEncoder

    torch.set_num_threads(Config.ondevice_num_threads)
    torch.set_num_interop_threads(Config.ondevice_interop_threads)
    dtype = _resolve_dtype(Config.reranker_dtype, torch)
    if dtype is torch.bfloat16:
        fast, named = _bf16_is_fast(torch), _bf16_acceleration(torch)
        if fast:
            logger.info(f"Loading reranker at bfloat16, accelerated by this CPU ({named or 'measured'})")
        elif fast is False:
            # Emulated rather than accelerated. Not an error, and it still halves
            # the memory, but an operator who set this expecting a speed-up has
            # to be told: emulation measured 6-10x slower than float32.
            logger.warning(
                "RAG_RERANKER_DTYPE=bfloat16, but this CPU runs bfloat16 slower than float32, so it is "
                "being emulated in software. Memory is still halved; speed will be several times WORSE. "
                "Set RAG_RERANKER_DTYPE=float32 unless the memory saving is what you are after."
            )
        else:
            logger.warning("RAG_RERANKER_DTYPE=bfloat16 but its speed could not be probed on this CPU")
    model = CrossEncoder(
        Config.reranker_model,
        max_length=Config.reranker_max_length,
        device="cpu",
        model_kwargs={"torch_dtype": dtype},
    )

    if Config.reranker_quantize:
        # Measured on ARM, int8 is slower and larger than fp32, so quantization is
        # opt-in rather than the default — turn it on only where it has been
        # measured to help.
        if dtype is not torch.float32:
            # quantize_dynamic reads fp32 weights; handing it bf16 either raises
            # or silently produces a model that scores differently.
            logger.warning(
                f"Ignoring RAG_RERANKER_QUANTIZE: dynamic quantization needs float32, not {Config.reranker_dtype}"
            )
        else:
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
        logger.info(
            f"Loading reranker model: {Config.reranker_model} "
            f"(dtype={Config.reranker_dtype}, quantize={Config.reranker_quantize})"
        )
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
    queued_at = time.monotonic()
    try:
        # Loading stays outside the semaphore: the first call spends ~35s pulling
        # weights, and holding an admission slot for that would stall every other
        # caller behind a one-off cost. Concurrent loads are already serialised by
        # ``_model_lock``.
        model = get_reranker()
        # page_content is typed str, but it arrives from a Qdrant payload that can
        # carry an explicit null; without the fallback one malformed document
        # would fail the whole batch closed.
        pairs = [(query, (d[0].page_content or "")[: Config.reranker_max_doc_chars]) for d in docs]
        # Timed from here, not from entry: the first call after a restart spends
        # ~35s in get_reranker, and counting that as queue wait would report a
        # cold start as contention.
        admitted_at = time.monotonic()
        with _inference_slots:
            waited = time.monotonic() - admitted_at
            # batch_size bounds peak activation memory: without it
            # sentence-transformers scores every pair in one batch, so the
            # footprint scaled with whatever retrieval happened to return.
            scored = _to_unit_scale(model.predict(pairs, batch_size=Config.reranker_batch_size))
    except Exception as e:
        # exc_info: this path discards the whole batch, so the traceback is the
        # only way to tell a model-load failure from a scoring one.
        logger.warning(f"Reranker failed to score documents: {e}", exc_info=True)
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
        f"(top={ranked[0][1]:.4f}, bottom={ranked[-1][1]:.4f}, "
        f"queued={waited:.2f}s, total={time.monotonic() - queued_at:.2f}s)"
    )
    return kept, True
