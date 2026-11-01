import logging
import time
from typing import Any, Optional

import tiktoken
from google import genai
from sqlalchemy import text

from rag.core.utils.db_query import engine
from utils.config import Config
from utils.shared import get_provider_name

logger = logging.getLogger(__name__)


def persist_search_embedding_usage(account_id: str, embedding_model, query: str) -> None:
    """Track token usage for search-time embed_query calls.

    Uses Google count_tokens API for accurate counts, falls back to char estimation.
    Writes to rag_embedding_token_usage with operation_type='search_query'.
    """
    try:
        provider = get_provider_name(embedding_model.__class__.__name__)
        model_id = getattr(embedding_model, "model_id", None) or getattr(embedding_model, "model", None)
        if model_id and str(model_id).startswith("models/"):
            model_id = str(model_id).replace("models/", "")

        # Estimate tokens
        tokens = max(1, len(query) // Config.avg_chars_per_token)
        if provider in ("googleai", "vertexai"):
            try:
                client = genai.Client(api_key=Config.embeddings_api_key)
                token_count = client.models.count_tokens(model=str(model_id), contents=query)
                if token_count.total_tokens is not None:
                    tokens = token_count.total_tokens
            except Exception:
                pass  # fall back to char estimation

        # Use shared engine from db_query (module-level singleton with connection pooling)
        with engine.connect() as connection:
            with connection.begin():
                connection.execute(
                    text("""
                        INSERT INTO rag_embedding_token_usage (
                            account_id, is_global, collection_name,
                            embedding_provider, embedding_model,
                            total_tokens, document_count,
                            operation_type, request_status
                        ) VALUES (
                            :account_id, :is_global, :collection_name,
                            :embedding_provider, :embedding_model,
                            :total_tokens, :document_count,
                            :operation_type, :request_status
                        );
                        """),
                    {
                        "account_id": account_id or "global",
                        "is_global": not bool(account_id) or account_id == "global",
                        "collection_name": "search_query",
                        "embedding_provider": provider,
                        "embedding_model": model_id,
                        "total_tokens": tokens,
                        "document_count": 1,
                        "operation_type": "search_query",
                        "request_status": "success",
                    },
                )
    except Exception as e:
        logger.error(f"[EmbeddingTracker] Failed to persist search embedding usage: {e}")


def persist_figure_description_usage(
    account_id: str,
    collection_name: str,
    provider: str,
    model: str,
    total_tokens: int,
    figure_count: int,
    status: str = "success",
    error_message: Optional[str] = None,
) -> None:
    """Track what describing a document's figures cost.

    Written to rag_embedding_token_usage like every other load-time cost this
    service incurs, under its own operation_type. Not
    llm_conversation_token_usage: that table requires a conversation_id and a
    message_id, and a knowledge-base load has neither — its existing caller
    skips the write rather than inventing them.

    The load-history query reads operation_type='batch_embedding' only, so these
    rows add a cost record without appearing as extra loads.
    """
    try:
        with engine.connect() as connection:
            with connection.begin():
                connection.execute(
                    text("""
                        INSERT INTO rag_embedding_token_usage (
                            account_id, is_global, collection_name,
                            embedding_provider, embedding_model,
                            total_tokens, document_count,
                            operation_type, request_status, error_message
                        ) VALUES (
                            :account_id, :is_global, :collection_name,
                            :provider, :model,
                            :total_tokens, :figure_count,
                            'figure_description', :status, :error_message
                        );
                        """),
                    {
                        "account_id": account_id or "global",
                        "is_global": not bool(account_id) or account_id == "global",
                        "collection_name": collection_name,
                        "provider": provider,
                        "model": model,
                        "total_tokens": total_tokens,
                        "figure_count": figure_count,
                        "status": status,
                        "error_message": error_message,
                    },
                )
    except Exception as e:
        logger.error(f"[EmbeddingTracker] Failed to persist figure description usage: {e}")


def persist_kb_classification_usage(
    account_id: str,
    collection_name: str,
    provider: str,
    model: str,
    total_tokens: int,
    document_count: int,
    duration_seconds: float,
    status: str = "success",
    error_message: Optional[str] = None,
) -> None:
    """Track what the KB capability-catalog classification LLM calls cost
    (documents/processing.py:_classify_source_documents) — one row per
    process_documents call, aggregated across every source document
    classified in it (one LLM call each).

    Same table/pattern as persist_figure_description_usage above: written to
    rag_embedding_token_usage under its own operation_type so it doesn't
    appear as an extra "load" to the load-history query (which reads
    operation_type='batch_embedding' only), while still being queryable for
    cost/duration on its own.
    """
    try:
        with engine.connect() as connection:
            with connection.begin():
                connection.execute(
                    text("""
                        INSERT INTO rag_embedding_token_usage (
                            account_id, is_global, collection_name,
                            embedding_provider, embedding_model,
                            total_tokens, document_count,
                            operation_type, request_status, error_message,
                            load_duration_seconds
                        ) VALUES (
                            :account_id, :is_global, :collection_name,
                            :provider, :model,
                            :total_tokens, :document_count,
                            'kb_metadata_classification', :status, :error_message,
                            :duration_seconds
                        );
                        """),
                    {
                        "account_id": account_id or "global",
                        "is_global": not bool(account_id) or account_id == "global",
                        "collection_name": collection_name,
                        "provider": provider,
                        "model": model,
                        "total_tokens": total_tokens,
                        "document_count": document_count,
                        "status": status,
                        "error_message": error_message,
                        "duration_seconds": duration_seconds,
                    },
                )
    except Exception as e:
        logger.error(f"[EmbeddingTracker] Failed to persist KB classification usage: {e}")


class EmbeddingTracker:
    def __init__(
        self,
        account_id,
        collection_name,
        knowledgebase_id=None,
        triggered_by="system",
        trigger_type="system_sync",
        module=None,
        expected_document_count=None,
        run_id=None,
    ):
        self.total_tokens = 0
        # Chunks embedded. Drives the token-rate logging, which is per
        # embedded unit.
        self.total_docs = 0
        # Source documents embedded. A long document is embedded as several
        # chunks, so counting units would report the Load History column in
        # chunks — it read in documents until chunking was introduced.
        self._document_keys: set = set()
        self.account_id = account_id if account_id else "global"
        self.is_global = not bool(account_id) or account_id == "global"
        self.collection_name = collection_name
        self.llm_provider = None
        self.llm_model = None
        self.knowledgebase_id = knowledgebase_id
        self.triggered_by = triggered_by
        self.trigger_type = trigger_type
        self.module = module
        self.expected_document_count = expected_document_count
        # Identifies this load. Stamped on every point indexed in it too, so "which
        # documents did this run touch" is a filtered query rather than a stored manifest.
        self.run_id = run_id
        self.load_start_time = None

    def record(self, *, doc_id, text, embedding_model, usage_metadata=None, document_key=None):
        """Capture provider, token usage, and model details."""
        if self.llm_provider is None:
            self.llm_provider = get_provider_name(embedding_model.__class__.__name__)
        if self.llm_model is None:
            model = getattr(embedding_model, "model_id", None)
            if not model:
                model = getattr(embedding_model, "model", None)
                if model:
                    if str(model).startswith("models/"):
                        model = str(model).replace("models/", "")
            self.llm_model = model

        if usage_metadata and "total_tokens" in usage_metadata:
            tokens = usage_metadata["total_tokens"]
        else:
            tokens = self._estimate_tokens(text, self.llm_model, self.llm_provider)

        self.total_tokens += tokens
        self.total_docs += 1
        self._document_keys.add(document_key or doc_id)
        logger.info(
            f"[EmbeddingTracker] Added {tokens} tokens for {doc_id}. "
            f"Total tokens for collection '{self.collection_name}': {self.total_tokens}"
        )

    def start_timer(self):
        """Call before document processing begins to track load duration."""
        self.load_start_time = time.time()

    def persist(self, status="success", error_message=None):
        """Persist the aggregated token count and load metadata to the database."""
        # A successful load that embedded nothing is a real, useful result: it
        # means every document was already current. Since the existence probe
        # landed that is the *expected* steady state, so suppressing the row
        # would leave the sync history silently stale — indistinguishable from
        # a sync that never ran. Only skip when no load was started at all,
        # which is what start_timer having never fired indicates.
        if self.total_tokens == 0 and status == "success" and self.load_start_time is None:
            logger.info("[EmbeddingTracker] No load was started; nothing to persist.")
            return

        load_duration = None
        if self.load_start_time is not None:
            load_duration = time.time() - self.load_start_time

        document_count = len(self._document_keys)

        try:
            # Use shared engine from db_query (module-level singleton with connection pooling)
            with engine.connect() as connection:
                with connection.begin():
                    insert_query = text("""
                        INSERT INTO rag_embedding_token_usage (
                            account_id, is_global, collection_name,
                            embedding_provider, embedding_model,
                            total_tokens, document_count,
                            operation_type, request_status, error_message,
                            knowledgebase_id, load_duration_seconds,
                            triggered_by, trigger_type, module,
                            expected_document_count, batch_id
                        )
                        VALUES (
                            :account_id, :is_global, :collection_name,
                            :embedding_provider, :embedding_model,
                            :total_tokens, :document_count,
                            :operation_type, :request_status, :error_message,
                            :knowledgebase_id, :load_duration_seconds,
                            :triggered_by, :trigger_type, :module,
                            :expected_document_count, :batch_id
                        );
                        """)
                    connection.execute(
                        insert_query,
                        {
                            "account_id": self.account_id,
                            "is_global": self.is_global,
                            "collection_name": self.collection_name,
                            "embedding_provider": self.llm_provider,
                            "embedding_model": self.llm_model,
                            "total_tokens": self.total_tokens,
                            "document_count": document_count,
                            "operation_type": "batch_embedding",
                            "request_status": status,
                            "error_message": error_message,
                            "knowledgebase_id": self.knowledgebase_id,
                            "load_duration_seconds": load_duration,
                            "triggered_by": self.triggered_by,
                            "trigger_type": self.trigger_type,
                            "module": self.module,
                            "expected_document_count": self.expected_document_count,
                            "batch_id": self.run_id,
                        },
                    )
                    logger.info(
                        f"[EmbeddingTracker] Inserted record for collection "
                        f"'{self.collection_name}' with {self.total_tokens} tokens, "
                        f"{document_count} documents, duration={load_duration:.1f}s"
                        if load_duration
                        else f"[EmbeddingTracker] Inserted record for collection "
                        f"'{self.collection_name}' with {self.total_tokens} tokens, "
                        f"{document_count} documents"
                    )

            self.total_tokens = 0
            self.total_docs = 0
            self._document_keys = set()

        except Exception as e:
            logger.error(f"[EmbeddingTracker] Failed to persist records: {e}")

    def _estimate_tokens(self, text: str, model_name: str, provider: str):
        """Estimate tokens depending on embedding provider.

        For bulk ingestion, uses local approximation to avoid per-document HTTP calls.
        Google AI's countTokens API is called only for small batches (< 100 docs tracked).
        """
        try:
            if provider in ("openai", "azure"):
                if not hasattr(self, "_tiktoken_enc"):
                    self._tiktoken_enc = tiktoken.encoding_for_model(model_name)
                return len(self._tiktoken_enc.encode(text))

            elif provider in ("googleai", "vertexai"):
                # During bulk ingestion (large collections), use local char-based approximation
                # to avoid 35K+ individual HTTP round-trips to countTokens API.
                # For small batches, use the accurate API.
                if self.total_docs < Config.token_count_api_threshold:
                    if not hasattr(self, "_google_client"):
                        self._google_client = genai.Client(api_key=Config.embeddings_api_key)
                    token_count = self._google_client.models.count_tokens(model=model_name, contents=text)
                    return token_count.total_tokens
                else:
                    # Release the Google client once we switch to local estimation
                    # to free httpx connection pool memory during bulk processing.
                    # Log the transition once so operators know token counts are
                    # approximate from this point forward.
                    if hasattr(self, "_google_client"):
                        logger.info(
                            f"[EmbeddingTracker] Switching to local token estimation "
                            f"after {self.total_docs} docs "
                            f"(threshold: {Config.token_count_api_threshold})"
                        )
                        del self._google_client
                    return max(1, len(text) // Config.avg_chars_per_token)

            elif provider in ("huggingface", "ollama"):
                # Avoid repeated tokenizer loading which is very memory intensive
                if not hasattr(self, "_hf_tokenizer"):
                    try:
                        from transformers import AutoTokenizer

                        self._hf_tokenizer: Any = AutoTokenizer.from_pretrained(model_name, use_fast=True)
                    except Exception as e:
                        logger.warning(f"[EmbeddingTracker] Failed to load tokenizer {model_name}: {e}")
                        self._hf_tokenizer = None

                if self._hf_tokenizer:
                    return len(self._hf_tokenizer.encode(text))

                return max(1, len(text) // Config.avg_chars_per_token)

            elif provider in ("bedrock", "sagemaker", "ondevice"):
                return max(1, len(text) // Config.avg_chars_per_token)

            else:
                return len(text.split())

        except Exception as e:
            logger.warning(f"[EmbeddingTracker] Token estimation failed for {provider}: {e}")
            return len(text.split())
