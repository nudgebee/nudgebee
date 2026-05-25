"""On-device embedding provider.

Supports two backends (controlled by ONDEVICE_BACKEND env var):
- "torch" (default): sentence-transformers with dynamic int8 quantization. Best accuracy, ~500MB memory.
- "onnx": ONNX Runtime with dynamic int8 quantization. Lower accuracy, ~250MB memory. No torch dependency.
"""

import gc
import logging
import os
import threading
from typing import TYPE_CHECKING, List

from rag.core.types import Embeddings

from utils.config import Config

if TYPE_CHECKING:
    import numpy as np

logger = logging.getLogger(__name__)

_model_instance = None
_model_lock = threading.Lock()


def _get_torch_model():
    """Load sentence-transformers model with torch int8 quantization."""
    import platform

    import torch
    from sentence_transformers import SentenceTransformer

    model_name = Config.embeddings_model_id
    truncate_dim = Config.embeddings_dimensions

    torch.set_num_threads(Config.ondevice_num_threads)
    torch.set_num_interop_threads(Config.ondevice_interop_threads)
    model = SentenceTransformer(model_name, truncate_dim=truncate_dim, device="cpu")

    # Dynamic int8 quantization: fp32 Linear weights -> int8
    # qnnpack for ARM (macOS Apple Silicon), fbgemm for x86 (Linux K8s)
    qengine = "qnnpack" if platform.machine() == "arm64" else "fbgemm"
    torch.backends.quantized.engine = qengine
    transformer = model[0]
    transformer.auto_model = torch.quantization.quantize_dynamic(
        transformer.auto_model, {torch.nn.Linear}, dtype=torch.qint8
    )
    model.eval()
    gc.collect()
    return model


def _get_onnx_model():
    """Load ONNX model with int8 quantization. No torch dependency."""
    import onnxruntime as ort  # type: ignore[import-untyped]
    from huggingface_hub import hf_hub_download
    from tokenizers import Tokenizer  # type: ignore[import-untyped]

    model_name = Config.embeddings_model_id

    # Download and quantize ONNX model (one-time)
    cache_dir = os.path.join(os.path.expanduser("~/.cache"), "rag-server", "onnx")
    os.makedirs(cache_dir, exist_ok=True)
    safe_name = model_name.replace("/", "_")
    quant_path = os.path.join(cache_dir, f"{safe_name}_qint8.onnx")

    if not os.path.exists(quant_path):
        from onnxruntime.quantization import QuantType, quantize_dynamic  # type: ignore[import-untyped]

        fp32_path = hf_hub_download(model_name, "onnx/model.onnx")
        logger.info(f"Quantizing ONNX model to int8: {fp32_path} -> {quant_path}")
        quantize_dynamic(fp32_path, quant_path, weight_type=QuantType.QInt8)
        gc.collect()

    session = ort.InferenceSession(quant_path, providers=["CPUExecutionProvider"])

    # Load tokenizer
    tok_path = hf_hub_download(model_name, "tokenizer.json")
    tokenizer = Tokenizer.from_file(tok_path)
    tokenizer.enable_padding()
    tokenizer.enable_truncation(max_length=512)

    return {"session": session, "tokenizer": tokenizer}


def _get_model():
    """Get or create singleton model instance."""
    global _model_instance
    if _model_instance is not None:
        return _model_instance

    with _model_lock:
        if _model_instance is not None:
            return _model_instance

        import psutil

        backend = Config.ondevice_backend.lower()
        model_name = Config.embeddings_model_id
        mem_before = psutil.Process(os.getpid()).memory_info().rss / 1024 / 1024
        logger.info(f"Loading on-device embedding model: {model_name} (backend={backend})")

        if backend == "onnx":
            _model_instance = _get_onnx_model()
        else:
            _model_instance = _get_torch_model()

        mem_after = psutil.Process(os.getpid()).memory_info().rss / 1024 / 1024
        logger.info(
            f"On-device embedding model loaded ({backend}): {model_name} "
            f"(memory: {mem_before:.0f}MB -> {mem_after:.0f}MB, model cost: {mem_after - mem_before:.0f}MB)"
        )
        return _model_instance


def _onnx_encode(model_dict: dict, texts: List[str], truncate_dim: int | None) -> "np.ndarray":
    """Encode texts using ONNX session with mean pooling + normalization."""
    import numpy as np

    session = model_dict["session"]
    tokenizer = model_dict["tokenizer"]

    encoded = tokenizer.encode_batch(texts)
    input_ids = np.array([e.ids for e in encoded], dtype=np.int64)
    attention_mask = np.array([e.attention_mask for e in encoded], dtype=np.int64)
    token_type_ids = np.zeros_like(input_ids)

    outputs = session.run(
        None, {"input_ids": input_ids, "attention_mask": attention_mask, "token_type_ids": token_type_ids}
    )

    # Mean pooling
    token_embeddings = outputs[0]
    mask_expanded = attention_mask[:, :, np.newaxis].astype(np.float32)
    embeddings = (token_embeddings * mask_expanded).sum(axis=1) / mask_expanded.sum(axis=1)

    # Normalize
    embeddings = embeddings / np.linalg.norm(embeddings, axis=1, keepdims=True)

    # Truncate dimensions (Matryoshka)
    if truncate_dim:
        embeddings = embeddings[:, :truncate_dim]

    return np.asarray(embeddings, dtype=np.float32)


class OnDeviceEmbeddings(Embeddings):
    """On-device embeddings with torch (default) or ONNX backend."""

    def _log_memory(self, operation: str, count: int):
        import psutil

        proc = psutil.Process(os.getpid())
        rss = proc.memory_info().rss / 1024 / 1024
        logger.info(f"[OnDeviceEmbeddings] {operation} ({count} texts) | RSS: {rss:.0f}MB")

    def embed_documents(self, texts: List[str]) -> List[List[float]]:
        model = _get_model()
        self._log_memory("embed_documents:start", len(texts))

        if isinstance(model, dict):
            # ONNX backend
            embeddings = _onnx_encode(model, texts, Config.embeddings_dimensions)
        else:
            # torch backend
            import torch

            with torch.no_grad():
                embeddings = model.encode(
                    texts,
                    normalize_embeddings=True,
                    show_progress_bar=False,
                    batch_size=Config.ondevice_batch_size,
                )

        self._log_memory("embed_documents:done", len(texts))
        result: List[List[float]] = embeddings.astype("float32").tolist()
        return result

    def embed_query(self, text: str) -> List[float]:
        model = _get_model()
        self._log_memory("embed_query:start", 1)

        if isinstance(model, dict):
            # ONNX backend
            embeddings = _onnx_encode(model, [text], Config.embeddings_dimensions)
            embedding = embeddings[0]
        else:
            # torch backend
            import torch

            with torch.no_grad():
                if callable(getattr(model, "encode_query", None)):
                    embedding = model.encode_query(text, normalize_embeddings=True)
                else:
                    embedding = model.encode(text, normalize_embeddings=True, show_progress_bar=False)

        self._log_memory("embed_query:done", 1)
        result: List[float] = embedding.astype("float32").tolist()
        return result
